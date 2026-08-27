// Command agent-leakprobe drives the nested-agent interrupt scenario against
// a running from-source engine and inspects that engine's goroutines at each
// step — the controlled twin of the live staff-session wedge:
//
//	chief (replay model) --tool--> hirer.hire (Dang module fn)
//	    --> spawns worker, worker's turn dwells in a slow container exec
//	    --> hire blocks in worker.send(task).await
//	interrupt chief mid-hire, then watch what unwinds and what leaks.
//
// Run from the workspace root against an engine with its debug server up
// (e.g. the engine-lab / ./hack/dev engine):
//
//	go build -o bin/dagger ./cmd/dagger
//	go run ./hack/agent-leakprobe tcp://<engine-host>:1234 http://<engine-host>:6060
//
// Interpretation notes from its first run (2026-08): on interrupt, the
// chief's chain unwinds at every seam (MCP.Call's cache wait, the Dang
// hire's nested awaitMessage, the module executor) while the WORKER's turn
// keeps running by design — a mid-turn worker shows the exact
// loop-in-pool.Wait / MCP.Call-in-Cache.wait / Dang-nested-query trio that a
// leak would, so a goroutine dump alone cannot distinguish a leak from a
// surviving worker turn. The final census after the worker's turn ends is
// the honest verdict: only idle loops should remain.
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"dagger.io/dagger"
	"github.com/tidwall/gjson"
)

var (
	runnerHost = os.Args[1]
	debugAddr  = os.Args[2]
)

const (
	task        = "leakprobe worker task"
	workerReply = "worker turn done"
	chiefPrompt = "leakprobe chief prompt"
	chiefReply  = "chief turn done"
)

func main() {
	ctx := context.Background()

	cwd, err := os.Getwd()
	must("getwd", err)
	os.Setenv("_EXPERIMENTAL_DAGGER_RUNNER_HOST", runnerHost)
	os.Setenv("_EXPERIMENTAL_DAGGER_CLI_BIN", filepath.Join(cwd, "bin", "dagger"))

	c, err := dagger.Connect(ctx, dagger.WithLogOutput(io.Discard))
	must("connect", err)
	defer c.Close()
	fmt.Println("== connected")

	// Serve the hirer module (spawns + awaits a worker inside module calls).
	must("serve hirer", c.ModuleSource(filepath.Join(cwd, "core/integration/testdata/modules/dang/agent-hirer")).AsModule().Serve(ctx))

	hirerID := q(ctx, c, `{ hirer { id } }`, nil, "hirer.id")
	fmt.Println("== hirer served")

	// Worker seed: a replay conversation that dispatches the bound slow
	// container's stdout tool, dwelling ~60s while heartbeating.
	vol := fmt.Sprintf("leakprobe-%d", time.Now().UnixNano())
	slowCtr := c.Container().From("alpine:3.20").
		WithMountedCache("/sync", c.CacheVolume(vol)).
		WithEnvVariable("CACHEBUSTER", vol).
		WithExec([]string{"sh", "-c",
			"i=0; while [ $i -lt 120 ]; do date +%s%N > /sync/beat.tmp && mv /sync/beat.tmp /sync/beat; i=$((i+1)); sleep 0.5; done; echo TOOL-DONE"})
	slowCtrID, err := slowCtr.ID(ctx)
	must("slow ctr id", err)

	workerModel := replayModel(ctx, c, c.LLM().
		WithPrompt(task).
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: "Dispatching the slow tool."},
			{Kind: dagger.LLMContentBlockKindToolCall, CallID: "call_1", ToolName: "stdout"},
		}).
		WithToolResult("call_1", "", false).
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: workerReply},
		}))

	workerSeedID := q(ctx, c, `query($model: String!, $tool: ID!) {
		llm(model: $model) { withTools(object: $tool) { id } }
	}`, map[string]any{"model": workerModel, "tool": slowCtrID}, "llm.withTools.id")
	fmt.Println("== worker seed composed")

	// Chief: a replay conversation whose one tool call is hirer.hire with
	// the worker seed — the module call that spawns, messages and AWAITS the
	// worker, i.e. the staff ask shape.
	hireArgs, err := json.Marshal(map[string]string{
		"seed": workerSeedID,
		"name": "hired",
		"task": task,
	})
	must("hire args", err)
	chiefModel := replayModel(ctx, c, c.LLM().
		WithPrompt(chiefPrompt).
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: "Hiring."},
			{Kind: dagger.LLMContentBlockKindToolCall, CallID: "call_1", ToolName: "hire", Arguments: dagger.JSON(hireArgs)},
		}).
		WithToolResult("call_1", "", false).
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: chiefReply},
		}))

	chiefID := q(ctx, c, `query($model: String!, $tools: ID!) {
		llm(model: $model) { withTools(object: $tools) { spawn(name: "chief") } }
	}`, map[string]any{"model": chiefModel, "tools": hirerID}, "llm.withTools.spawn")
	fmt.Println("== chief spawned")

	delivery := q(ctx, c, `query($id: ID!, $msg: String!) {
		node(id: $id) { ... on Agent { send(message: $msg) } }
	}`, map[string]any{"id": chiefID, "msg": chiefPrompt}, "node.send")
	_ = delivery
	fmt.Println("== chief prompted (turn opening)")

	// Wait until the worker's dwell is provably underway (first beat).
	readBeat := func() string {
		out, err := c.Container().From("alpine:3.20").
			WithMountedCache("/sync", c.CacheVolume(vol)).
			WithEnvVariable("CACHEBUSTER", fmt.Sprint(time.Now().UnixNano())).
			WithExec([]string{"sh", "-c", "cat /sync/beat 2>/dev/null || true"}).
			Stdout(ctx)
		must("read beat", err)
		return strings.TrimSpace(out)
	}
	start := time.Now()
	for readBeat() == "" {
		if time.Since(start) > 90*time.Second {
			fatal("worker dwell never started")
		}
		time.Sleep(time.Second)
	}
	fmt.Println("== worker dwelling in slow tool; chief blocked in hire")

	dump("MID-FLIGHT (before interrupt)")

	// Interrupt the chief mid-hire.
	q(ctx, c, `query($id: ID!) { node(id: $id) { ... on Agent { interrupt } } }`,
		map[string]any{"id": chiefID}, "node.interrupt")
	fmt.Println("== chief interrupted; waiting for PAUSED")
	parkCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	state := q(parkCtx, c, `query($id: ID!) { node(id: $id) { ... on Agent { waitFor(state: PAUSED) state } } }`,
		map[string]any{"id": chiefID}, "node.state")
	cancel()
	fmt.Println("== chief state after interrupt:", state)

	time.Sleep(5 * time.Second)
	dump("T+5s AFTER INTERRUPT")
	time.Sleep(15 * time.Second)
	dump("T+20s AFTER INTERRUPT")

	// Let the worker's dwell run out (120 beats x 0.5s from its start),
	// then take the final census: only idle loops should remain.
	fmt.Println("== waiting out the worker dwell...")
	lastBeat := readBeat()
	for i := 0; i < 40; i++ {
		time.Sleep(3 * time.Second)
		b := readBeat()
		if b == lastBeat {
			break
		}
		lastBeat = b
	}
	dump("AFTER WORKER DWELL ENDS")

	fmt.Println("== done (session still open at final dump)")
}

// dump fetches the engine's goroutines and prints a classified census plus
// the full stacks of chain-relevant goroutines.
func dump(label string) {
	fmt.Printf("\n===== DUMP: %s =====\n", label)
	resp, err := http.Get(debugAddr + "/debug/pprof/goroutine?debug=2")
	must("dump", err)
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	must("dump read", err)

	interesting := regexp.MustCompile(`AgentRuntime|MCP\)\.Call|waitForLazyEvaluation|Cache\)\.wait\(|evaluateOne\.func|dangshared|ContainerExecState|messageAwait|ServeHTTPToNestedClient`)
	classifier := []struct{ name string; re *regexp.Regexp }{
		{"loop-in-CallBatch", regexp.MustCompile(`MCP\)\.CallBatch[\s\S]*AgentRuntime\)\.loop`)},
		{"loop-idle-or-step", regexp.MustCompile(`AgentRuntime\)\.loop`)},
		{"MCP.Call-in-cache-wait", regexp.MustCompile(`Cache\)\.wait\([\s\S]*MCP\)\.Call`)},
		{"awaitMessage", regexp.MustCompile(`AgentRuntime\)\.awaitMessage`)},
		{"dang-nested-server", regexp.MustCompile(`ServeHTTPToNestedClient`)},
		{"dang-eval", regexp.MustCompile(`dangshared|vito/dang`)},
		{"lazy-eval-wait", regexp.MustCompile(`waitForLazyEvaluation`)},
		{"lazy-eval-owner", regexp.MustCompile(`evaluateOne\.func`)},
		{"container-exec", regexp.MustCompile(`ContainerExecState`)},
	}

	blocks := strings.Split(string(body), "\n\n")
	counts := map[string]int{}
	shown := 0
	for _, b := range blocks {
		if !interesting.MatchString(b) {
			continue
		}
		for _, cl := range classifier {
			if cl.re.MatchString(b) {
				counts[cl.name]++
				break
			}
		}
		// Print a compact signature: goroutine header + dagger/dang frames.
		lines := strings.Split(b, "\n")
		var sig []string
		for _, l := range lines {
			if strings.HasPrefix(l, "goroutine ") ||
				strings.Contains(l, "dagger/core") ||
				strings.Contains(l, "dagger/dagql") ||
				strings.Contains(l, "vito/dang") ||
				strings.Contains(l, "engine/server") {
				if !strings.HasPrefix(l, "\t") {
					sig = append(sig, "  "+strings.TrimSpace(l))
				}
			}
		}
		if shown < 40 {
			fmt.Println(strings.Join(sig, "\n"))
			fmt.Println("  ---")
			shown++
		}
	}
	fmt.Println("counts:", counts, "total goroutines:", len(blocks))
}

func replayModel(ctx context.Context, c *dagger.Client, llm *dagger.LLM) string {
	id, err := llm.ID(ctx)
	must("llm id", err)
	raw := qraw(ctx, c, `query($llm: ID!){node(id:$llm){... on LLM{messages{role content{kind text callId toolName arguments errored signature} tokenUsage{inputTokens outputTokens cachedTokenReads cachedTokenWrites totalTokens}}}}}`,
		map[string]any{"llm": string(id)})
	msgs := gjson.Get(raw, "node.messages")
	return "replay/" + base64.StdEncoding.EncodeToString([]byte(msgs.Raw))
}

func q(ctx context.Context, c *dagger.Client, query string, vars map[string]any, path string) string {
	raw := qraw(ctx, c, query, vars)
	out := gjson.Get(raw, path)
	if !out.Exists() {
		fatal(fmt.Sprintf("no %s in response: %s", path, raw))
	}
	return out.String()
}

func qraw(ctx context.Context, c *dagger.Client, query string, vars map[string]any) string {
	res := map[string]any{}
	err := c.Do(ctx, &dagger.Request{Query: query, Variables: vars}, &dagger.Response{Data: &res})
	must("query: "+query[:min(60, len(query))], err)
	raw, err := json.Marshal(res)
	must("marshal", err)
	return string(raw)
}

func must(what string, err error) {
	if err != nil {
		fatal(what + ": " + err.Error())
	}
}

func fatal(msg string) {
	fmt.Fprintln(os.Stderr, "FATAL:", msg)
	os.Exit(1)
}
