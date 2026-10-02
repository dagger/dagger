package core

// A benchmark harness for applying tool changesets to an LLM's workspace
// (MCP.applyChangeset): one tool call per run, on a fresh value workspace, for
// a few realistic changeset shapes. It measures nothing itself beyond
// client-side wall clock; run it under engine-dev's testProfile so the test
// engine records wcprof, then report on the tool's call op:
//
//	engine-lab engineTest(pkg: "./core/integration",
//	  run: "TestLLM/TestBenchChangesetApply", wcprofCapture: "<name>")
//
// Skipped unless _DAGGER_BENCH is set (testProfile sets it), to keep it out
// of CI.

import (
	"context"
	"fmt"
	"os"
	"sort"
	"time"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/internal/buildkit/identity"
)

// benchEnv opts benchmark-style integration tests in.
const benchEnv = "_DAGGER_BENCH"

// The workspace: 5,000 files (22 MB) in 50 packages, plus a 2.4 MB
// generated file.
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

// The tools, one per scenario. Each returns a changeset with unique content,
// so no run hits the cache.
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

// TestBenchChangesetApply runs each scenario's tool once per rep, each run in
// its own client (and CLI session), so a wcprof capture can be scoped by
// client as well as rooted at the tool's call op (Bench.<scenario>). No step
// is forced to evaluate: the run ends with one read of the resulting
// workspace, which is where lazy work left by applying the changeset lands.
func (LLMSuite) TestBenchChangesetApply(ctx context.Context, t *testctx.T) {
	if os.Getenv(benchEnv) == "" {
		t.Skip("benchmark: set " + benchEnv + "=1 to run (engine-dev testProfile does)")
	}

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

	results := map[string][]benchSample{}
	for _, scenario := range scenarios {
		for rep := range reps {
			s := runChangesetBench(ctx, t, repoID, scenario, rep == reps-1)
			t.Logf("BENCH scenario=%s rep=%d %s", scenario, rep, s)
			if rep > 0 {
				results[scenario] = append(results[scenario], s)
			}
		}
	}
	for _, scenario := range scenarios {
		samples := results[scenario]
		median := func(get func(benchSample) time.Duration) time.Duration {
			vals := make([]time.Duration, len(samples))
			for i, s := range samples {
				vals[i] = get(s)
			}
			sort.Slice(vals, func(i, j int) bool { return vals[i] < vals[j] })
			return vals[len(vals)/2]
		}
		t.Logf("BENCH-MEDIAN scenario=%s loop=%dms read=%dms", scenario,
			median(func(s benchSample) time.Duration { return s.loop }).Milliseconds(),
			median(func(s benchSample) time.Duration { return s.read }).Milliseconds())
	}
}

type benchSample struct {
	// loop is the agent loop: the tool call and applying its changeset.
	loop time.Duration
	// read is the one read of the resulting workspace.
	read time.Duration
	// recipe describes the resulting recipe, when it was captured.
	recipe string
}

func (s benchSample) String() string {
	return fmt.Sprintf("loop=%dms read=%dms%s", s.loop.Milliseconds(), s.read.Milliseconds(), s.recipe)
}

// runChangesetBench makes one tool call on a fresh workspace in its own
// client, then reads the resulting workspace once.
func runChangesetBench(ctx context.Context, t *testctx.T, repoID dagger.ID, scenario string, captureRecipe bool) benchSample {
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

	var s benchSample
	result := composed.WithPrompt("go").Loop()
	start := time.Now()
	transcript, err := result.Transcript(ctx)
	s.loop = time.Since(start)
	require.NoError(t, err)
	require.Contains(t, transcript, "done")
	require.NotContains(t, transcript, "rror")

	start = time.Now()
	_, err = result.Workspace().Directory("/").Entries(ctx)
	s.read = time.Since(start)
	require.NoError(t, err)

	if captureRecipe {
		recipe, err := sink.captureLLMRecipe(ctx, t, c, result)
		require.NoError(t, err)
		id := new(call.ID)
		require.NoError(t, id.Decode(string(recipe)))
		counts := map[string]int{}
		countIDFields(id, counts, map[string]bool{})
		s.recipe = fmt.Sprintf(" recipe_bytes=%d withExec=%d withChanges=%d Workspace.withPatchFile=%d Directory.withPatchFile=%d Workspace.withDirectory=%d Workspace.withoutDirectory=%d",
			len(recipe), counts["withExec"], counts["withChanges"],
			counts["Workspace.withPatchFile"], counts["Directory.withPatchFile"],
			counts["Workspace.withDirectory"], counts["Workspace.withoutDirectory"])
	}
	// Close now rather than at test cleanup, so runs don't overlap.
	require.NoError(t, c.Close())
	return s
}

// countIDFields counts the distinct calls in a recipe by field name.
func countIDFields(id *call.ID, into map[string]int, seen map[string]bool) {
	for cur := id; cur != nil; cur = cur.Receiver() {
		dig := cur.Digest().String()
		if seen[dig] {
			return
		}
		seen[dig] = true
		into[cur.Field()]++
		if recv := cur.Receiver(); recv != nil && recv.Type() != nil {
			// Also by type, e.g. Workspace.withPatchFile apart from
			// Directory.withPatchFile.
			into[recv.Type().NamedType()+"."+cur.Field()]++
		}
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
