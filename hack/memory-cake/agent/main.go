// Command agent is the memory cake's synthetic coding agent: a scripted MCP
// client, with no LLM, that drives one long `dagger mcp` session the way our
// coding agents drive theirs, and takes heap profiles of the engine at
// checkpoints.
//
// Every turn reads files, edits the workspace (a Go package and a Dang
// module that grow every turn), builds, vets and tests with test telemetry,
// calls tool modules whose state grows with every call, loads the edited
// module again, and every few turns inspects the session's trace the way an
// agent does (FindSpans, FindCalls, ReadLogs, ReadTrace, InspectCall). The
// script is deterministic: the same turns run in the same order on every
// engine.
//
// At turn 0 and every -checkpoint turns, it fetches the engine's heap
// profile after a forced GC (/debug/pprof/heap?gc=1) and its memstats
// (/debug/vars), buckets the profile into the cake's layers (see
// hack/memory-cake/layers) and writes a JSON checkpoint under -out.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/pprof/profile"
	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/dagger/dagger/hack/memory-cake/layers"
)

var (
	side         = flag.String("side", "before", "label of the engine build under test")
	turns        = flag.Int("turns", 5, "turns to run")
	every        = flag.Int("checkpoint", 5, "take a heap checkpoint every this many turns")
	inspectEvery = flag.Int("inspect", 3, "inspect the trace every this many turns")
	outDir       = flag.String("out", "/out", "directory checkpoints are written under (in <side>/)")
	workdir      = flag.String("workdir", "/work", "the workspace `dagger mcp` serves")
	bin          = flag.String("bin", "dagger", "dagger CLI")
	debugURL     = flag.String("debug", "http://dagger-engine:6060", "the engine's debug server")
	callTimeout  = flag.Duration("call-timeout", 30*time.Minute, "timeout of one tool call")
	linger       = flag.Bool("linger", false, "keep the session open after the last turn")
)

func main() {
	flag.Parse()
	a := &agent{dir: filepath.Join(*outDir, *side), start: time.Now()}
	if err := os.MkdirAll(filepath.Join(a.dir, "raw"), 0o755); err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(a.dir, "checkpoints"), 0o755); err != nil {
		log.Fatal(err)
	}
	if err := a.run(context.Background()); err != nil {
		a.status("failed: " + err.Error())
		log.Fatal(err)
	}
}

type agent struct {
	dir   string
	start time.Time
	cli   *mcpclient.Client
	tools map[string]bool
	calls *os.File
	turn  int
	errs  int
	// spanID and digest are the latest a FindSpans and a FindCalls
	// returned, for the inspection calls that need one.
	spanID, digest string
}

func (a *agent) run(ctx context.Context) error {
	calls, err := os.OpenFile(filepath.Join(a.dir, "calls.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	a.calls = calls
	defer calls.Close()

	a.status("starting dagger mcp")
	if err := a.connect(ctx); err != nil {
		return err
	}
	if err := a.checkpoint(ctx); err != nil {
		return fmt.Errorf("checkpoint at turn 0: %w", err)
	}
	for a.turn = 1; a.turn <= *turns; a.turn++ {
		a.status("running")
		if err := a.runTurn(ctx); err != nil {
			return fmt.Errorf("turn %d: %w", a.turn, err)
		}
		if a.turn%*every == 0 || a.turn == *turns {
			if err := a.checkpoint(ctx); err != nil {
				return fmt.Errorf("checkpoint at turn %d: %w", a.turn, err)
			}
		}
	}
	a.turn--
	a.status("done")
	if *linger {
		select {}
	}
	return nil
}

// connect spawns `dagger mcp` and completes the MCP handshake.
func (a *agent) connect(ctx context.Context) error {
	stderr, err := os.Create(filepath.Join(a.dir, "mcp-stderr.log"))
	if err != nil {
		return err
	}
	stdio := transport.NewStdioWithOptions(*bin, nil, []string{"--progress=plain", "mcp"},
		transport.WithCommandFunc(func(ctx context.Context, command string, env []string, args []string) (*exec.Cmd, error) {
			cmd := exec.CommandContext(ctx, command, args...)
			cmd.Dir = *workdir
			cmd.Env = directEngineEnv(append(os.Environ(), env...))
			return cmd, nil
		}))
	cli := mcpclient.NewClient(stdio)
	if err := cli.Start(ctx); err != nil {
		return fmt.Errorf("start dagger mcp: %w", err)
	}
	// Plain progress logs every span for hours: keep the start of it.
	go func() {
		_, _ = io.Copy(&capped{w: stderr, left: 32 << 20}, stdio.Stderr())
	}()
	initCtx, cancel := context.WithTimeout(ctx, 45*time.Minute)
	defer cancel()
	if _, err := cli.Initialize(initCtx, mcp.InitializeRequest{
		Params: mcp.InitializeParams{ClientInfo: mcp.Implementation{Name: "memory-cake", Version: "0.0.1"}},
	}); err != nil {
		return fmt.Errorf("initialize MCP session: %w", err)
	}
	a.cli = cli
	res, err := cli.ListTools(initCtx, mcp.ListToolsRequest{})
	if err != nil {
		return fmt.Errorf("list tools: %w", err)
	}
	a.tools = map[string]bool{}
	var list strings.Builder
	for _, t := range res.Tools {
		a.tools[t.Name] = true
		fmt.Fprintf(&list, "== %s ==\n%s\n", t.Name, t.Description)
		if schema, err := json.Marshal(t.InputSchema); err == nil {
			fmt.Fprintf(&list, "input schema: %s\n", schema)
		}
		list.WriteString("\n")
	}
	return os.WriteFile(filepath.Join(a.dir, "tools.txt"), []byte(list.String()), 0o644)
}

// capped writes the first left bytes to w and discards the rest.
type capped struct {
	w    io.Writer
	left int
}

func (c *capped) Write(p []byte) (int, error) {
	if c.left > 0 {
		n := min(len(p), c.left)
		c.left -= n
		if _, err := c.w.Write(p[:n]); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

// directEngineEnv drops the session an exec gets to the engine running it,
// so the CLI connects to _EXPERIMENTAL_DAGGER_RUNNER_HOST: the engine under
// test (see engine-dev's withoutOuterSession).
func directEngineEnv(env []string) []string {
	filtered := make([]string, 0, len(env))
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		switch key {
		case "DAGGER_SESSION_PORT", "DAGGER_SESSION_TOKEN", "DAGGER_SESSION_CLIENT_ID", "DAGGER_ENGINE_NUM_CPU":
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

// tool resolves a tool's served name from "<namespace>.<name>" (or a bare
// builtin name): an object's tools are served bare unless a name collides
// with another object's, and then all of its tools are namespaced, e.g.
// "cake_test".
func (a *agent) tool(name string) string {
	ns, bare, ok := strings.Cut(name, ".")
	if !ok {
		return name
	}
	if a.tools[ns+"_"+bare] {
		return ns + "_" + bare
	}
	return bare
}

type callRecord struct {
	Turn    int     `json:"turn"`
	Tool    string  `json:"tool"`
	Args    string  `json:"args,omitempty"`
	Seconds float64 `json:"seconds"`
	Error   bool    `json:"error,omitempty"`
	Bytes   int     `json:"bytes"`
	Head    string  `json:"head"`
}

// call calls a tool and returns its result text. A tool error is recorded
// and returned as the result: an agent reads errors too. Only a dead session
// is an error.
func (a *agent) call(ctx context.Context, name string, args map[string]any) (string, error) {
	name = a.tool(name)
	ctx, cancel := context.WithTimeout(ctx, *callTimeout)
	defer cancel()
	started := time.Now()
	res, err := a.cli.CallTool(ctx, mcp.CallToolRequest{Params: mcp.CallToolParams{Name: name, Arguments: args}})
	rec := callRecord{Turn: a.turn, Tool: name, Seconds: time.Since(started).Seconds()}
	if raw, err := json.Marshal(args); err == nil {
		rec.Args = clip(string(raw), 200)
	}
	var text string
	switch {
	case err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded):
		// A stuck call: the session lives on.
		text = "timed out: " + err.Error()
		rec.Error = true
	case err != nil:
		rec.Error = true
		rec.Head = err.Error()
		a.record(rec)
		return "", fmt.Errorf("call %s: %w", name, err)
	default:
		var b strings.Builder
		for _, c := range res.Content {
			if t, ok := c.(mcp.TextContent); ok {
				b.WriteString(t.Text)
				b.WriteString("\n")
			}
		}
		text = b.String()
		rec.Error = res.IsError
	}
	if rec.Error {
		a.errs++
	}
	rec.Bytes = len(text)
	rec.Head = clip(text, 600)
	a.record(rec)
	log.Printf("turn %d: %s (%.1fs, %d bytes%s)", a.turn, name, rec.Seconds, rec.Bytes, map[bool]string{true: ", ERROR"}[rec.Error])
	return text, nil
}

func (a *agent) record(rec callRecord) {
	line, _ := json.Marshal(rec)
	a.calls.Write(append(line, '\n'))
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

type status struct {
	Side    string  `json:"side"`
	Turn    int     `json:"turn"`
	Turns   int     `json:"turns"`
	State   string  `json:"state"`
	Errors  int     `json:"errors"`
	Elapsed float64 `json:"elapsedSeconds"`
	Time    string  `json:"time"`
}

func (a *agent) status(state string) {
	data, _ := json.MarshalIndent(status{
		Side: *side, Turn: a.turn, Turns: *turns, State: state, Errors: a.errs,
		Elapsed: time.Since(a.start).Seconds(), Time: time.Now().UTC().Format(time.RFC3339),
	}, "", "  ")
	_ = writeFileAtomic(filepath.Join(a.dir, "status.json"), data)
}

func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ---------------------------------------------------------------- checkpoints

// Checkpoint is one heap measurement of the engine.
type Checkpoint struct {
	Side     string           `json:"side"`
	Turn     int              `json:"turn"`
	Time     string           `json:"time"`
	Elapsed  float64          `json:"elapsedSeconds"`
	Errors   int              `json:"toolErrors"`
	Heap     *layers.Result   `json:"heap"`
	MemStats map[string]int64 `json:"memstats"`
}

func (a *agent) checkpoint(ctx context.Context) error {
	a.status(fmt.Sprintf("checkpoint at turn %d", a.turn))
	raw, err := get(ctx, *debugURL+"/debug/pprof/heap?gc=1")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(a.dir, "raw", fmt.Sprintf("heap-t%03d.pb.gz", a.turn)), raw, 0o644); err != nil {
		return err
	}
	p, err := profile.ParseData(raw)
	if err != nil {
		return fmt.Errorf("parse heap profile: %w", err)
	}
	heap, err := layers.Bucket(p, 15)
	if err != nil {
		return err
	}
	vars, err := get(ctx, *debugURL+"/debug/vars")
	if err != nil {
		return err
	}
	var v struct {
		MemStats map[string]any `json:"memstats"`
	}
	if err := json.Unmarshal(vars, &v); err != nil {
		return fmt.Errorf("parse /debug/vars: %w", err)
	}
	ms := map[string]int64{}
	for _, k := range []string{"HeapAlloc", "HeapInuse", "HeapIdle", "HeapReleased", "HeapSys", "Sys", "StackInuse", "NumGC"} {
		if f, ok := v.MemStats[k].(float64); ok {
			ms[k] = int64(f)
		}
	}
	cp := Checkpoint{
		Side: *side, Turn: a.turn, Time: time.Now().UTC().Format(time.RFC3339),
		Elapsed: time.Since(a.start).Seconds(), Errors: a.errs, Heap: heap, MemStats: ms,
	}
	data, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		return err
	}
	log.Printf("checkpoint turn %d: heap %.1f MiB, HeapInuse %.1f MiB", a.turn, float64(heap.TotalBytes)/(1<<20), float64(ms["HeapInuse"])/(1<<20))
	return writeFileAtomic(filepath.Join(a.dir, "checkpoints", fmt.Sprintf("t%03d.json", a.turn)), data)
}

func get(ctx context.Context, url string) ([]byte, error) {
	var lastErr error
	for range 10 {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			body, rerr := io.ReadAll(resp.Body)
			resp.Body.Close()
			cancel()
			if rerr == nil && resp.StatusCode == http.StatusOK {
				return body, nil
			}
			err = fmt.Errorf("GET %s: %s: %v %s", url, resp.Status, rerr, clip(string(body), 200))
		} else {
			cancel()
		}
		lastErr = err
		time.Sleep(5 * time.Second)
	}
	return nil, lastErr
}

// ---------------------------------------------------------------- the turn

// readFiles are the files the agent reads, three a turn, round robin.
var readFiles = []string{
	"README.md",
	"go.mod",
	"pkg/dang/eval.go",
	"pkg/hm/types.go",
	"pkg/dang/infer.go",
	"pkg/introspection/introspection.go",
	"pkg/dang/stdlib.go",
	"pkg/querybuilder/querybuilder.go",
	"pkg/ioctx/context.go",
	"pkg/hm/unify.go",
	"pkg/dang/session.go",
	"pkg/dang/types.go",
}

var spanIDRe = regexp.MustCompile(`\b[0-9a-f]{16}\b`)
var digestRe = regexp.MustCompile(`xxh3:[0-9a-f]+`)

func (a *agent) runTurn(ctx context.Context) error {
	t := a.turn
	step := func(name string, args map[string]any) (string, error) {
		return a.call(ctx, name, args)
	}

	// Look around.
	for i := range 3 {
		if _, err := step("editor.read", map[string]any{"filePath": readFiles[(3*t+i)%len(readFiles)], "limit": 300}); err != nil {
			return err
		}
	}
	if t%4 == 0 {
		if _, err := step("editor.grep", map[string]any{"regex": "func New", "glob": "pkg/hm/*.go", "content": true}); err != nil {
			return err
		}
	}

	// Read what the agent wrote so far, in its bound workspace.
	if t > 1 {
		if _, err := step("DangEval", map[string]any{"source": `ws.file("pkg/cake/turns.go").contents(limitLines: 200)`}); err != nil {
			return err
		}
	}

	// Edit: a Go package that grows a function and a test every turn.
	//
	// `dagger mcp` serves tool calls without the conversation's bound
	// workspace (it calls each tool directly, not through MCP.Call), so a
	// Workspace-taking tool such as the editor's write sees the host
	// checkout every time and edits never accumulate. DangEval reads and
	// writes the bound workspace (`ws`), so the agent edits through it.
	// The first turn writes the files; every turn then edits them as the
	// editor's edit does, File.withReplaced on the workspace's file, so the
	// workspace is a chain of small edits.
	if t == 1 {
		if _, err := step("DangEval", map[string]any{"source": fmt.Sprintf(
			"ws = ws.withNewFile(\"pkg/cake/turns.go\", %s).withNewFile(\"pkg/cake/turns_test.go\", %s)\nws.directory(\"pkg/cake\").entries",
			dangString(goFile(0)), dangString(testFile(0)))}); err != nil {
			return err
		}
	}
	if _, err := step("DangEval", map[string]any{"source": fmt.Sprintf(
		"ws = ws.withFile(\"pkg/cake/turns.go\", ws.file(\"pkg/cake/turns.go\").withReplaced(%s, %s))\nws = ws.withFile(\"pkg/cake/turns_test.go\", ws.file(\"pkg/cake/turns_test.go\").withReplaced(%s, %s))\nws.directory(\"pkg/cake\").entries",
		dangString(marker), dangString(turnFunc(t)+marker), dangString(marker), dangString(turnTest(t)+marker))}); err != nil {
		return err
	}

	// Build, vet and test the bound workspace, with test telemetry.
	if _, err := step("DangEval", map[string]any{"source": `cake.build(source: ws, vet: ["./pkg/cake/...", "./pkg/ioctx/..."])`}); err != nil {
		return err
	}
	if _, err := step("DangEval", map[string]any{"source": `cake.test(source: ws, pkgs: ["./pkg/cake/...", "./pkg/hm/...", "./pkg/querybuilder/..."])`}); err != nil {
		return err
	}

	// Tools whose state grows with every call.
	if _, err := step("cake.note", map[string]any{"text": noteText(t)}); err != nil {
		return err
	}
	if _, err := step("context.blockWrite", map[string]any{
		"id": fmt.Sprintf("turns/%03d", t), "kind": "NOTE", "text": blockText(t), "tags": []string{"memory-cake", fmt.Sprintf("turn-%d", t)},
	}); err != nil {
		return err
	}
	if _, err := step("cakego.add", map[string]any{"n": t}); err != nil {
		return err
	}

	// Develop a module: edit it and load it again.
	if t == 1 {
		if _, err := step("DangEval", map[string]any{"source": fmt.Sprintf(
			"ws = ws.withNewFile(\".dagger/modules/probe/main.dang\", %s)\nws.directory(\".dagger/modules/probe\").entries", dangString(probeFile(0)))}); err != nil {
			return err
		}
	}
	if _, err := step("DangEval", map[string]any{"source": fmt.Sprintf(
		"ws = ws.withFile(\".dagger/modules/probe/main.dang\", ws.file(\".dagger/modules/probe/main.dang\").withReplaced(%s, %s))\nws.directory(\".dagger/modules/probe\").entries",
		dangString(probeMarker), dangString(probeTurn(t)+probeMarker))}); err != nil {
		return err
	}
	// Load it as a module (its type definitions come from running it), and
	// evaluate it in place, as DangEval at a module does.
	if _, err := step("DangEval", map[string]any{"source": `let m = ws.directory(".dagger/modules/probe").asModule
[m.name, toString(m.objects.{{id}}.length)]`}); err != nil {
		return err
	}
	if _, err := step("DangEval", map[string]any{"at": ".dagger/modules/probe", "source": fmt.Sprintf("Probe().turn%d", t)}); err != nil {
		return err
	}

	// Inspect the trace.
	if t%*inspectEvery == 0 {
		if err := a.inspect(ctx, t); err != nil {
			return err
		}
	}
	if t%5 == 0 {
		if _, err := step("cake.summary", map[string]any{}); err != nil {
			return err
		}
		if _, err := step("context.compose", map[string]any{"tags": []string{"memory-cake"}}); err != nil {
			return err
		}
	}
	return nil
}

func (a *agent) inspect(ctx context.Context, t int) error {
	out, err := a.call(ctx, "FindSpans", map[string]any{"query": fmt.Sprintf("TestTurn%d", t)})
	if err != nil {
		return err
	}
	if id := spanIDRe.FindString(out); id != "" {
		a.spanID = id
	}
	if _, err := a.call(ctx, "FindSpans", map[string]any{"query": "", "status": "failed", "limit": 20}); err != nil {
		return err
	}
	if _, err := a.call(ctx, "FindSpans", map[string]any{"query": "test", "match": "path", "limit": 50}); err != nil {
		return err
	}
	if a.spanID != "" {
		if _, err := a.call(ctx, "ReadLogs", map[string]any{"span": a.spanID, "limit": 100}); err != nil {
			return err
		}
		if _, err := a.call(ctx, "ReadTrace", map[string]any{"span": a.spanID}); err != nil {
			return err
		}
		if _, err := a.call(ctx, "ReadTrace", map[string]any{"span": a.spanID, "view": "timings", "limit": 100}); err != nil {
			return err
		}
	}
	out, err = a.call(ctx, "FindCalls", map[string]any{"query": "withReplaced|withNewFile", "limit": 50})
	if err != nil {
		return err
	}
	if d := digestRe.FindString(out); d != "" {
		a.digest = d
	}
	if a.digest != "" {
		if _, err := a.call(ctx, "InspectCall", map[string]any{"digest": a.digest, "view": "stats"}); err != nil {
			return err
		}
	}
	if _, err := a.call(ctx, "ListServices", map[string]any{}); err != nil {
		return err
	}
	return nil
}

// filler is deterministic text of about n bytes for turn t.
func filler(t, n int) string {
	var b bytes.Buffer
	for i := 0; b.Len() < n; i++ {
		fmt.Fprintf(&b, "turn %d line %d: the quick brown fox jumps over the lazy dog. ", t, i)
	}
	return b.String()[:n]
}

func turnFunc(t int) string {
	return fmt.Sprintf("// Turn%d returns turn %d's text.\nfunc Turn%d() string {\n\treturn %q\n}\n\n", t, t, t, filler(t, 1024))
}

func turnTest(t int) string {
	return fmt.Sprintf("func TestTurn%d(t *testing.T) {\n\tif len(Turn%d()) != 1024 {\n\t\tt.Fatalf(\"turn %d: got %%d bytes\", len(Turn%d()))\n\t}\n\tt.Log(\"turn %d ok\")\n}\n\n", t, t, t, t, t)
}

// The markers each turn's edits insert before.
const (
	marker      = "// cake: next turn"
	probeMarker = "# cake: next turn"
)

// goFile is pkg/cake/turns.go after turn t: a function per turn.
func goFile(t int) string {
	var b strings.Builder
	b.WriteString("// Package cake grows a function every turn of the memory cake.\npackage cake\n\n")
	for i := 1; i <= t; i++ {
		b.WriteString(turnFunc(i))
	}
	return b.String() + marker + "\n"
}

// testFile is pkg/cake/turns_test.go after turn t: a test per turn.
func testFile(t int) string {
	var b strings.Builder
	b.WriteString("package cake\n\nimport \"testing\"\n\n")
	for i := 1; i <= t; i++ {
		b.WriteString(turnTest(i))
	}
	return b.String() + marker + "\n"
}

// probeFile is the probe module after turn t: a function per turn.
func probeFile(t int) string {
	var b strings.Builder
	b.WriteString("# A module the memory cake's synthetic agent edits every turn and loads\n# again, as an agent developing a module does: every edit is a new module.\n\n")
	b.WriteString("\"\"\"\nGrows by a function each turn.\n\"\"\"\ntype Probe {\n  \"\"\"\n  The base of every turn's greeting.\n  \"\"\"\n  greeting: String! {\n    \"hello from probe\"\n  }\n\n  ")
	for i := 1; i <= t; i++ {
		b.WriteString(probeTurn(i))
	}
	b.WriteString(probeMarker + "\n}\n")
	return b.String()
}

// probeTurn is the probe module's function for turn t.
func probeTurn(t int) string {
	return fmt.Sprintf("\n  \"\"\"\n  Turn %d's greeting.\n  \"\"\"\n  turn%d: String! {\n    greeting + \" at turn %d\"\n  }\n\n  ", t, t, t)
}

// dangString quotes s as a Dang string literal.
func dangString(s string) string {
	if strings.Contains(s, "$") {
		panic("dangString: $ would interpolate")
	}
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\t", `\t`)
	return `"` + r.Replace(s) + `"`
}

func noteText(t int) string {
	return fmt.Sprintf("Turn %d: edited pkg/cake, built, vetted and tested it. %s", t, filler(t, 2048))
}

func blockText(t int) string {
	return fmt.Sprintf("What turn %d established: Turn%d returns 1024 bytes and its test passes. %s", t, t, filler(t, 1536))
}
