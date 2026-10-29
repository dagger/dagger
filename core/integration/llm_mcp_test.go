package core

// Tests for external MCP servers attached with LLM.withMCPServer.

import (
	"context"
	"time"

	"dagger.io/dagger/core"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// counterMCPServer is a minimal stdio MCP server with one tool, count, that
// reports how many calls this server process has served. A conversation that
// reuses one MCP session sees the count go up; one that dials a fresh server
// sees it start over.
const counterMCPServer = `package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
)

func main() {
	calls := 0
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 1<<20), 1<<20)
	out := json.NewEncoder(os.Stdout)
	for in.Scan() {
		var req struct {
			ID     json.RawMessage ` + "`json:\"id\"`" + `
			Method string          ` + "`json:\"method\"`" + `
			Params struct {
				ProtocolVersion string ` + "`json:\"protocolVersion\"`" + `
			} ` + "`json:\"params\"`" + `
		}
		if err := json.Unmarshal(in.Bytes(), &req); err != nil {
			panic(err)
		}
		if req.ID == nil {
			continue // a notification
		}
		resp := map[string]any{"jsonrpc": "2.0", "id": req.ID}
		switch req.Method {
		case "initialize":
			resp["result"] = map[string]any{
				"protocolVersion": req.Params.ProtocolVersion,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "counter", "version": "1.0.0"},
			}
		case "tools/list":
			resp["result"] = map[string]any{"tools": []any{map[string]any{
				"name":        "count",
				"description": "Count the calls this server has served.",
				"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
			}}}
		case "tools/call":
			calls++
			resp["result"] = map[string]any{"content": []any{map[string]any{
				"type": "text",
				"text": fmt.Sprintf("count=%d", calls),
			}}}
		default:
			resp["error"] = map[string]any{"code": -32601, "message": "method not found: " + req.Method}
		}
		if err := out.Encode(resp); err != nil {
			panic(err)
		}
	}
}
`

// TestMCPServerSessionAcrossSteps locks in that a loop's steps share one MCP
// session: the session the first step opens serves the second step's tool
// call, rather than the second step dialing the server again.
func (LLMSuite) TestMCPServerSessionAcrossSteps(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	srv := core.NewQuery(c).Container().From(golangImage).
		WithEnvVariable("CGO_ENABLED", "0").
		WithNewFile("/src/main.go", counterMCPServer).
		WithWorkdir("/src").
		WithExec([]string{"go", "build", "-o", "/usr/local/bin/counter", "main.go"}).
		WithWorkdir("/").
		WithDefaultArgs([]string{"counter"}).
		AsService()

	// Two steps that each call count, then a closing reply.
	const prompt = "Call the count tool twice, then say how it went."
	model := cannedRecordingModel(ctx, t, c, core.NewQuery(c).LLM().
		WithPrompt(prompt).
		WithResponse([]core.LLMContentBlockInput{
			{Kind: core.LLMContentBlockKindToolCall, CallID: "call_1", ToolName: "count", Arguments: core.JSON(`{}`)},
		}).
		WithToolResult("call_1", "", false).
		WithResponse([]core.LLMContentBlockInput{
			{Kind: core.LLMContentBlockKindToolCall, CallID: "call_2", ToolName: "count", Arguments: core.JSON(`{}`)},
		}).
		WithToolResult("call_2", "", false).
		WithResponse([]core.LLMContentBlockInput{
			{Kind: core.LLMContentBlockKindText, Text: "Counted twice."},
		}))

	// A lost session used to hang the second step forever.
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	transcript, err := core.NewQuery(c).LLM(core.LLMOpts{Model: model}).
		WithMCPServer("counter", srv).
		WithPrompt(prompt).
		Loop().
		Transcript(ctx)
	require.NoError(t, err)
	require.Contains(t, transcript, "count=1")
	require.Contains(t, transcript, "count=2")
	require.Contains(t, transcript, "Counted twice.")
}
