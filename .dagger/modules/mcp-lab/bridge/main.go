// Command mcp-bridge exposes a stdio MCP server (`dagger mcp`) over HTTP so
// Dagger-module tools can drive a persistent MCP session interactively.
//
// A model talks to `dagger mcp` over stdio: one long-lived process holding one
// session, in which services started by earlier tool calls stay alive for
// later ones. Dagger-module tools can't hold a subprocess's stdio open across
// calls, so this bridge does: it spawns the CLI once, performs the MCP
// initialize handshake, and serves the session over HTTP — the same trick
// DAGGER_TUI_CONSOLE plays for the TUI.
//
//	GET  /tools  tools/list, rendered verbatim (name, description, schema)
//	POST /call   {"name": ..., "arguments": {...}} -> raw tool result text,
//	             prefixed with an isError marker when the call failed
//
// It reuses the repo's own mcp-go dependency, so the client always speaks the
// same protocol version as the CLI under test. The CLI's stderr (progress,
// engine chatter) passes through to the bridge's stderr, landing in the
// service's logs where session tools can read it.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

func main() {
	port := flag.Int("port", 7788, "HTTP port to listen on")
	workdir := flag.String("workdir", ".", "working directory for the spawned CLI")
	bin := flag.String("bin", "dagger", "CLI binary to spawn")
	flag.Parse()
	args := flag.Args() // e.g. --progress=plain mcp [extra flags]

	b := &bridge{ready: make(chan struct{})}
	go b.connect(*bin, *workdir, args)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /", b.handleHelp)
	mux.HandleFunc("GET /tools", b.handleTools)
	mux.HandleFunc("POST /call", b.handleCall)

	log.Printf("mcp-bridge listening on :%d (spawning %q %s in %s)",
		*port, *bin, strings.Join(args, " "), *workdir)
	if err := http.ListenAndServe(fmt.Sprintf(":%d", *port), mux); err != nil {
		log.Fatal(err)
	}
}

type bridge struct {
	cli     *mcpclient.Client
	initErr error
	ready   chan struct{} // closed once connect finishes (either way)
}

// connect spawns the CLI and completes the MCP handshake. Failures are
// recorded rather than fatal so the HTTP server stays up to report them —
// otherwise the service's port healthcheck fails with an opaque error.
func (b *bridge) connect(bin, workdir string, args []string) {
	defer close(b.ready)

	stdio := transport.NewStdioWithOptions(
		bin,
		nil,
		args,
		transport.WithCommandFunc(func(ctx context.Context, command string, env []string, cmdArgs []string) (*exec.Cmd, error) {
			cmd := exec.CommandContext(ctx, command, cmdArgs...)
			cmd.Dir = workdir
			cmd.Env = append(os.Environ(), env...)
			return cmd, nil
		}),
	)
	cli := mcpclient.NewClient(stdio)

	ctx := context.Background()
	if err := cli.Start(ctx); err != nil {
		b.initErr = fmt.Errorf("start MCP transport: %w", err)
		return
	}

	// Relay the CLI's stderr (progress, engine chatter) into the service logs.
	go func() {
		_, _ = io.Copy(os.Stderr, stdio.Stderr())
	}()

	// Generous timeout: first initialization may build SDKs in the engine.
	initCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	if _, err := cli.Initialize(initCtx, mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ClientInfo: mcp.Implementation{
				Name:    "mcp-bridge",
				Version: "0.0.1",
			},
		},
	}); err != nil {
		b.initErr = fmt.Errorf("initialize MCP session: %w", err)
		return
	}

	b.cli = cli
	log.Printf("MCP session initialized")
}

// session blocks until the handshake settles and reports any init failure to
// the client. A nil return means the response has already been written.
func (b *bridge) session(w http.ResponseWriter, r *http.Request) *mcpclient.Client {
	select {
	case <-b.ready:
	case <-r.Context().Done():
		http.Error(w, "request canceled while waiting for MCP session init", http.StatusServiceUnavailable)
		return nil
	}
	if b.initErr != nil {
		fmt.Fprintf(w, "MCP session failed to initialize: %v\n", b.initErr)
		return nil
	}
	return b.cli
}

func (b *bridge) handleHelp(w http.ResponseWriter, _ *http.Request) {
	fmt.Fprint(w, `mcp-bridge: HTTP frontend to a live "dagger mcp" stdio session.
GET  /tools  list the session's tools verbatim
POST /call   {"name": ..., "arguments": {...}} to call one
`)
}

func (b *bridge) handleTools(w http.ResponseWriter, r *http.Request) {
	cli := b.session(w, r)
	if cli == nil {
		return
	}
	res, err := cli.ListTools(r.Context(), mcp.ListToolsRequest{})
	if err != nil {
		fmt.Fprintf(w, "tools/list failed: %v\n", err)
		return
	}
	for _, tool := range res.Tools {
		fmt.Fprintf(w, "== %s ==\n", tool.Name)
		if tool.Description != "" {
			fmt.Fprintln(w, tool.Description)
		}
		if schema, err := json.Marshal(tool.InputSchema); err == nil {
			fmt.Fprintf(w, "input schema: %s\n", schema)
		}
		fmt.Fprintln(w)
	}
}

func (b *bridge) handleCall(w http.ResponseWriter, r *http.Request) {
	cli := b.session(w, r)
	if cli == nil {
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		fmt.Fprintf(w, "read request body: %v\n", err)
		return
	}
	var req struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		fmt.Fprintf(w, "bad request body %q: %v\n", body, err)
		return
	}
	var args map[string]any
	if len(req.Arguments) > 0 {
		if err := json.Unmarshal(req.Arguments, &args); err != nil {
			fmt.Fprintf(w, "arguments must be a JSON object, got %q: %v\n", req.Arguments, err)
			return
		}
	}

	res, err := cli.CallTool(r.Context(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      req.Name,
			Arguments: args,
		},
	})
	if err != nil {
		// RPC-level failure (e.g. the CLI process died) — distinct from a
		// tool-level isError result, which the model sees as content.
		fmt.Fprintf(w, "tools/call RPC failed: %v\n", err)
		return
	}
	if res.IsError {
		fmt.Fprintln(w, "⚠ isError=true — the model receives this as a TOOL ERROR:")
	}
	for _, item := range res.Content {
		switch c := item.(type) {
		case mcp.TextContent:
			fmt.Fprintln(w, c.Text)
		default:
			fmt.Fprintf(w, "(non-text content: %T)\n", item)
		}
	}
}
