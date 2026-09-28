package core

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// TestNodeLoadCost isolates what a node(id:) load of a runtime handle costs,
// and what it scales with.
//
// Loading a handle should be a lookup: the result is already attached, and
// the handle names it directly. The core node loader first resolves which
// schema the result belongs to, though: it walks the result's whole call graph
// for Module references (ModDepsForCall) and builds a schema server with those
// modules installed. The server is memoized only on the SchemaBuilder that
// ModDepsForCall returns fresh on every call, so a value that references user
// modules pays a full schema build, installing every module's typedefs, on
// every load.
//
// The interactive agent hits this constantly: its status line and changes
// panel each reload the conversation's LLM handle, and that LLM references
// every tool module the agent was composed with.
//
// Each scenario is an LLM handle varying one factor, loaded repeatedly the way
// the TUI does it; a core Directory handle with no module references is the
// control.
func (LLMSuite) TestNodeLoadCost(ctx context.Context, t *testctx.T) {
	const (
		numModules   = 8
		numFunctions = 12
		historyTurns = 200
		turnsPerStep = 25
		samples      = 15
	)

	c := connect(ctx, t)

	do := func(query string, vars map[string]any, out any) {
		t.Helper()
		require.NoError(t, c.Do(ctx, &dagger.Request{Query: query, Variables: vars}, &dagger.Response{Data: out}))
	}

	// Tool modules with enough functions and arguments to give each a real
	// set of typedefs to install.
	fixture := c.Directory()
	var toml strings.Builder
	names := make([]string, numModules)
	for i := range numModules {
		name := fmt.Sprintf("tools%d", i)
		names[i] = name
		fmt.Fprintf(&toml, "[modules.%s]\nsource = %q\n", name, name)
		var src strings.Builder
		fmt.Fprintf(&src, "type Tools%d {\n", i)
		for j := range numFunctions {
			fmt.Fprintf(&src, "  fn%d(a: String! = \"x\", b: Int! = 1, c: [String!]! = [], d: Boolean! = false): String! { a }\n", j)
		}
		src.WriteString("}\n")
		fixture = fixture.
			WithNewFile(name+"/dagger.json", fmt.Sprintf(`{"name":%q,"engineVersion":"v1.0.0-0","sdk":"dang"}`, name)).
			WithNewFile(name+"/main.dang", src.String())
	}
	ws := fixture.WithNewFile("dagger.toml", toml.String()).AsWorkspace()

	toolIDs := make([]string, numModules)
	for i, name := range names {
		require.NoError(t, ws.ModuleSource(name).AsModule().Serve(ctx))
		var res map[string]struct{ ID string }
		do(fmt.Sprintf(`{ %s { id } }`, name), nil, &res)
		toolIDs[i] = res[name].ID
		require.NotEmpty(t, toolIDs[i])
	}

	var bare struct{ LLM struct{ ID string } }
	do(`{ llm { id } }`, nil, &bare)

	withTools := func(id string, n int) string {
		for _, tool := range toolIDs[:n] {
			var res struct {
				Node struct{ WithTools struct{ ID string } }
			}
			do(`query($id: ID!, $tool: ID!) { node(id: $id) { ... on LLM { withTools(object: $tool) { id } } } }`,
				map[string]any{"id": id, "tool": tool}, &res)
			id = res.Node.WithTools.ID
		}
		return id
	}

	// History is appended a batch of turns per query, re-rooted on the
	// previous batch's handle, the way an agent's snapshots advance.
	withHistory := func(id string) string {
		for turn := 0; turn < historyTurns; turn += turnsPerStep {
			var sel strings.Builder
			for k := range turnsPerStep {
				fmt.Fprintf(&sel, `withPrompt(prompt: "prompt %d") { withResponse(content: [{kind: TEXT, text: "response %d"}], inputTokens: 10, outputTokens: 5) { `, turn+k, turn+k)
			}
			sel.WriteString("id")
			sel.WriteString(strings.Repeat(" } }", turnsPerStep))
			var res struct {
				Node map[string]any
			}
			do(`query($id: ID!) { node(id: $id) { ... on LLM { `+sel.String()+` } } }`,
				map[string]any{"id": id}, &res)
			v := any(res.Node)
			for range turnsPerStep {
				v = v.(map[string]any)["withPrompt"].(map[string]any)["withResponse"]
			}
			id = v.(map[string]any)["id"].(string)
		}
		return id
	}

	var dir struct {
		Directory struct{ WithNewFile struct{ ID string } }
	}
	do(`{ directory { withNewFile(path: "a", contents: "a") { id } } }`, nil, &dir)

	const llmFragment = `... on LLM { tokenUsage { inputTokens } }`
	moduleful := withTools(bare.LLM.ID, numModules)
	scenarios := []struct {
		name, id, fragment string
		// modules marks the scenarios the assertion covers: their cost
		// must not grow with the modules the value references.
		modules bool
	}{
		{"directory (control)", dir.Directory.WithNewFile.ID, `... on Directory { entries }`, false},
		{"llm", bare.LLM.ID, llmFragment, false},
		{fmt.Sprintf("llm + %d turns", historyTurns), withHistory(bare.LLM.ID), llmFragment, false},
		{"llm + 1 module", withTools(bare.LLM.ID, 1), llmFragment, true},
		{fmt.Sprintf("llm + %d modules", numModules/2), withTools(bare.LLM.ID, numModules/2), llmFragment, true},
		{fmt.Sprintf("llm + %d modules", numModules), moduleful, llmFragment, true},
		{fmt.Sprintf("llm + %d modules + %d turns", numModules, historyTurns), withHistory(moduleful), llmFragment, true},
	}

	// Samples are interleaved round-robin, so load from tests running in
	// parallel lands on every scenario alike.
	durs := make([][]time.Duration, len(scenarios))
	for range samples {
		for i, sc := range scenarios {
			var res struct{ Node any }
			start := time.Now()
			do(`query($id: ID!) { node(id: $id) { `+sc.fragment+` } }`, map[string]any{"id": sc.id}, &res)
			durs[i] = append(durs[i], time.Since(start))
			require.NotNil(t, res.Node, sc.name)
		}
	}

	mins := make([]time.Duration, len(scenarios))
	var report strings.Builder
	fmt.Fprintf(&report, "\n%-32s %10s %10s %10s %10s\n", "scenario", "first", "p50", "min", "max")
	for i, sc := range scenarios {
		first := durs[i][0]
		slices.Sort(durs[i])
		mins[i] = durs[i][0]
		fmt.Fprintf(&report, "%-32s %10s %10s %10s %10s\n", sc.name,
			first.Round(10*time.Microsecond), durs[i][samples/2].Round(10*time.Microsecond),
			mins[i].Round(10*time.Microsecond), durs[i][samples-1].Round(10*time.Microsecond))
	}
	t.Log(report.String())

	// A handle load should cost about the same whatever the value references.
	// Compare floors: the per-module cost is paid on every load, so it raises
	// the minimum, while noise from a busy engine only raises the rest.
	baseline := mins[1]
	budget := max(3*baseline, baseline+5*time.Millisecond)
	for i, sc := range scenarios {
		if !sc.modules {
			continue
		}
		require.LessOrEqualf(t, mins[i], budget,
			"node(id:) of %q takes at least %s vs %s for a bare LLM; loading a handle should not scale with the modules the value references%s",
			sc.name, mins[i], baseline, report.String())
	}
}
