package core

import (
	"context"
	"crypto/rand"
	"strings"

	"dagger.io/dagger/core"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// A Go module's first runtime build on a fresh engine finds the dependency
// calls for what generated code imports (distconsts.GoSDKSharedImports) in the
// engine image's entries, whole, though the module imports a package beyond
// them: only the call for that package runs. The shard that runs this suite
// ships the image's entries (test-split's testModuleRuntimes).
func (GoSuite) TestRuntimeFindsSharedDependenciesInImage(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	cloud := newTelemetrySplitCloud(t, c)
	engine, err := devEngineContainerAsService(telemetrySplitEngineWithoutCloud(c, devEngineContainer(c))).Start(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = engine.Stop(ctx) })

	// net/http/httptest is outside what generated code imports and reaches.
	_, err = telemetrySplitClient(ctx, t, c, daggerCliFile(t, c), engine, cloud).
		WithWorkdir("/work").
		WithNewFile("dagger.json", `{"name":"shared","engineVersion":"latest","sdk":{"source":"go"}}`).
		WithNewFile("main.go", "package main\n\nimport \"net/http/httptest\"\n\ntype Shared struct{}\n\nfunc (m *Shared) Hello() string { return httptest.DefaultRemoteAddr }\n").
		WithEnvVariable("CACHEBUSTER", rand.Text()).
		WithExec([]string{"/bin/dagger", "call", "-m", ".", "hello"}, core.ContainerWithExecOpts{DisableDaggerInDagger: true}).
		Stdout(ctx)
	require.NoError(t, err)

	type span struct {
		Name  string         `json:"name"`
		Attrs map[string]any `json:"attrs"`
	}
	var hits, ran int
	var outcomes []string
	for _, s := range readTelemetrySplitLines[span](ctx, t, cloud, "v1/traces.json.spans") {
		if !strings.HasSuffix(s.Name, "Gobuild.dependencies") {
			continue
		}
		outcome, _ := s.Attrs["dagger.io/cache.outcome"].(string)
		if outcome == "" {
			continue
		}
		outcomes = append(outcomes, outcome)
		if outcome == "hit" {
			hits++
		} else {
			ran++
		}
	}
	t.Logf("dependency call outcomes: %v", outcomes)
	// The standard library's shared call and the other shared call are the
	// image's; only the call for the module's own extra package runs. Without
	// the image's calls all three would run, and without the split there would
	// be one call, which would run.
	require.Equal(t, 2, hits, "the two shared dependency calls are found in the image")
	require.Equal(t, 1, ran, "only the call for the module's own extra package runs")
}
