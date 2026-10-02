package core

// BENCHMARK — not for merging. Times MCP.applyChangeset (changeset
// normalization + workspace overlay) for realistic tool changesets, from the
// engine's "bench.*" spans (core/mcp_bench.go).
//
//	engine-dev test --pkg ./core/integration --run TestLLM/TestBenchChangesetApply --test-verbose

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/internal/buildkit/identity"
)

const benchRepoScript = `set -e
mkdir -p /repo && cd /repo
awk 'BEGIN{
  for (p = 0; p < 50; p++) {
    dir = sprintf("pkg%02d", p)
    system("mkdir -p " dir)
    for (f = 0; f < 100; f++) {
      fn = sprintf("%s/f%02d.go", dir, f)
      for (i = 0; i < 100; i++) printf "// %s line %04d lorem ipsum dolor sit amet\n", fn, i > fn
      close(fn)
    }
  }
}'
mkdir -p gen
awk 'BEGIN{for (i = 0; i < 40000; i++) printf "func Generated%06d() string { return \"value %06d\" }\n", i, i}' > gen/big.gen.go
`

// A generator: rewrites a line of a large generated file, touches 20 sources
// and adds 5 files, like an SDK codegen run.
const benchCodegenScript = `set -e
old=$(printf 'value 0%s0100' "$GEN")
sed -i "s/$old\"/$old $NONCE\"/" gen/big.gen.go
for f in pkg0$GEN/f0*.go pkg0$GEN/f1*.go; do echo "// $NONCE" >> "$f"; done
mkdir -p gen/sdk$GEN
for i in 1 2 3 4 5; do echo "package sdk // $NONCE $i" > gen/sdk$GEN/new_$i.go; done
`

// Vendoring: 1000 new 4KB files.
const benchVendorScript = `set -e
awk -v n="$NONCE" 'BEGIN{
  for (d = 0; d < 20; d++) {
    dir = sprintf("vendor/mod%02d", d)
    system("mkdir -p " dir)
    for (f = 0; f < 50; f++) {
      fn = sprintf("%s/v%02d.go", dir, f)
      for (i = 0; i < 100; i++) printf "// %s %s line %04d lorem ipsum dolor\n", n, fn, i > fn
      close(fn)
    }
  }
}'
`

func benchGenerator(k int) string {
	return fmt.Sprintf(`
    let before%[1]d = container.from("alpine:3.22")
      .withWorkdir("/src")
      .withDirectory(".", ws.directory("/"))
      .withEnvVariable("GEN", "%[1]d")
      .withEnvVariable("NONCE", UUID.v7)
      .withExec(["true"])
    let gen%[1]d = before%[1]d.withExec(["sh", "-ec", %[2]q]).directory(".").changes(before%[1]d.directory("."))`,
		k, benchCodegenScript)
}

func benchModule() string {
	return fmt.Sprintf(`
type Bench {
  agent(base: LLM!): LLM! @agent {
    base.withTools(currentNode)
  }

  @cache(policy: FunctionCachePolicy.Never)
  edit(ws: Workspace!): Changeset! {
    let root = ws.directory("/")
    root.withNewFile("pkg01/f01.go", "edited " + UUID.v7 + "\n").changes(root)
  }

  @cache(policy: FunctionCachePolicy.Never)
  codegen(ws: Workspace!): Changeset! {%[1]s
    gen1
  }

  @cache(policy: FunctionCachePolicy.Never)
  codegenMerged(ws: Workspace!): Changeset! {%[1]s%[2]s%[3]s%[4]s
    gen1.withChangesets([gen2, gen3, gen4])
  }

  @cache(policy: FunctionCachePolicy.Never)
  vendor(ws: Workspace!): Changeset! {
    let before = container.from("alpine:3.22")
      .withWorkdir("/src")
      .withDirectory(".", ws.directory("/"))
      .withEnvVariable("NONCE", UUID.v7)
      .withExec(["true"])
    before.withExec(["sh", "-ec", %[5]q]).directory(".").changes(before.directory("."))
  }

  @cache(policy: FunctionCachePolicy.Never)
  noop(ws: Workspace!): Changeset! {
    let before = container.from("alpine:3.22")
      .withWorkdir("/src")
      .withDirectory(".", ws.directory("/"))
      .withEnvVariable("NONCE", UUID.v7)
    before.withExec(["true"]).directory(".").changes(before.directory("."))
  }
}
`, benchGenerator(1), benchGenerator(2), benchGenerator(3), benchGenerator(4), benchVendorScript)
}

func (LLMSuite) TestBenchChangesetApply(ctx context.Context, t *testctx.T) {
	const reps = 4 // the first is a warmup and is reported but not aggregated
	scenarios := []string{"edit", "codegen", "codegenMerged", "vendor", "noop"}

	setup := connect(ctx, t)
	repo := setup.Container().From(alpineImage).
		WithExec([]string{"sh", "-ec", benchRepoScript}).
		Directory("/repo")
	_, err := repo.Sync(ctx)
	require.NoError(t, err)
	repoID, err := repo.ID(ctx)
	require.NoError(t, err)

	type sample map[string]float64
	results := map[string][]sample{}
	for _, scenario := range scenarios {
		for rep := range reps {
			c, sink := connectWithTrace(ctx, t)
			ws := dagger.Ref[*dagger.Directory](c, repoID).
				WithNewFile("NONCE", identity.NewID()).
				WithNewFile("dagger.toml", "[modules.bench]\nsource = \"modules/bench\"\n").
				WithNewFile("modules/bench/dagger.json", `{"name":"bench","engineVersion":"v1.0.0-0","sdk":"dang"}`).
				WithNewFile("modules/bench/main.dang", benchModule()).
				AsWorkspace()
			model := cannedRecordingModel(ctx, t, c, c.LLM().
				WithPrompt("go").
				WithResponse([]dagger.LLMContentBlockInput{{
					Kind: dagger.LLMContentBlockKindToolCall, CallID: "call_1", ToolName: scenario,
				}}).
				WithToolResult("call_1", "", false).
				WithResponse([]dagger.LLMContentBlockInput{{Kind: dagger.LLMContentBlockKindText, Text: "done"}}))
			composed, err := composeArtifactAgents(ctx, c, ws, nil, c.LLM(dagger.LLMOpts{Model: model}).WithWorkspace(ws))
			require.NoError(t, err)
			result := composed.WithPrompt("go").Loop()
			start := time.Now()
			transcript, err := result.Transcript(ctx)
			loop := time.Since(start)
			require.NoError(t, err)
			require.Contains(t, transcript, "done")
			require.NotContains(t, transcript, "rror")

			var recipeInfo string
			if rep == reps-1 {
				recipe, err := sink.captureLLMRecipe(ctx, t, c, result)
				require.NoError(t, err)
				id := new(call.ID)
				require.NoError(t, id.Decode(string(recipe)))
				counts := map[string]int{}
				countIDFields(id, counts, map[string]bool{})
				recipeInfo = fmt.Sprintf(" recipe_bytes=%d withExec=%d withPatchFile=%d", len(recipe), counts["withExec"], counts["withPatchFile"])
			}
			require.NoError(t, c.Close())

			s := sample{"loop": float64(loop.Milliseconds())}
			traces, _ := sink.capture()
			for _, req := range traces {
				for _, rs := range req.ResourceSpans {
					for _, ss := range rs.ScopeSpans {
						for _, span := range ss.Spans {
							if !strings.HasPrefix(span.Name, "bench.") || span.EndTimeUnixNano == 0 {
								continue
							}
							s[strings.TrimPrefix(span.Name, "bench.")] += float64(span.EndTimeUnixNano-span.StartTimeUnixNano) / 1e6
						}
					}
				}
			}
			t.Logf("BENCH scenario=%s rep=%d %s%s", scenario, rep, formatSample(s), recipeInfo)
			if rep > 0 {
				results[scenario] = append(results[scenario], s)
			}
		}
	}
	for _, scenario := range scenarios {
		med := sample{}
		keys := map[string]bool{}
		for _, s := range results[scenario] {
			for k := range s {
				keys[k] = true
			}
		}
		for k := range keys {
			var vals []float64
			for _, s := range results[scenario] {
				vals = append(vals, s[k])
			}
			sort.Float64s(vals)
			med[k] = vals[len(vals)/2]
		}
		t.Logf("BENCH-MEDIAN scenario=%s %s", scenario, formatSample(med))
	}
}

func formatSample(s map[string]float64) string {
	order := []string{"apply", "normalize", "patch", "rebase", "base", "target", "check", "gitapply", "reconcile", "fallback", "count", "overlay", "loop"}
	var parts []string
	for _, k := range order {
		if v, ok := s[k]; ok {
			parts = append(parts, fmt.Sprintf("%s=%.0f", k, v))
		}
	}
	for k, v := range s {
		if !strings.Contains(" "+strings.Join(order, " ")+" ", " "+k+" ") {
			parts = append(parts, fmt.Sprintf("%s=%.0f", k, v))
		}
	}
	return strings.Join(parts, " ")
}

func countIDFields(id *call.ID, into map[string]int, seen map[string]bool) {
	for cur := id; cur != nil; cur = cur.Receiver() {
		dig := cur.Digest().String()
		if seen[dig] {
			return
		}
		seen[dig] = true
		into[cur.Field()]++
		for _, arg := range cur.Args() {
			countLiteralFields(arg.Value(), into, seen)
		}
	}
}

func countLiteralFields(lit call.Literal, into map[string]int, seen map[string]bool) {
	switch v := lit.(type) {
	case *call.LiteralID:
		countIDFields(v.Value(), into, seen)
	case *call.LiteralList:
		for _, item := range v.Values() {
			countLiteralFields(item, into, seen)
		}
	case *call.LiteralObject:
		for _, field := range v.Args() {
			if field != nil {
				countLiteralFields(field.Value(), into, seen)
			}
		}
	}
}
