package core

import (
	"context"
	"fmt"

	"dagger.io/dagger"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// Each exec.run span carries the content-preferred digest that later cache
// hits of the owning call report. A git tree learns its content only when it
// is evaluated, which can be after the withExec call span of the first run
// completes; the execution digest is read when the execution ends, so it
// identifies the same work as the hits. The command does not read the tree;
// the mount alone is an input of its identity.
func (EngineSuite) TestExecutionDigestMatchesCacheHits(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	// The client forwards the session telemetry to the fake Cloud.
	cloud := newTelemetrySplitCloud(t, c)
	engine, err := devEngineContainerAsService(telemetrySplitEngineWithoutCloud(c, devEngineContainer(c))).Start(ctx)
	require.NoError(t, err)

	marker := identity.NewID()
	script := fmt.Sprintf(
		`container | from %s | with-mounted-directory /src $(git https://github.com/dagger/dagger-test-modules | commit %s | tree) | with-exec echo %s | stdout`,
		alpineImage, vcsTestCaseCommit, marker,
	)
	// Two clients, so two sessions: the first executes, the second hits.
	for range 2 {
		out, err := telemetrySplitClient(ctx, t, c, daggerCliFile(t, c), engine, cloud).
			WithEnvVariable("CACHEBUSTER", identity.NewID()).
			WithExec([]string{"/bin/dagger", "script", "-M", "-c", script}, dagger.ContainerWithExecOpts{DisableDaggerInDagger: true}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, marker)
	}

	type span struct {
		Name  string            `json:"name"`
		Attrs map[string]string `json:"attrs"`
	}
	const (
		recipeAttr   = "dagger.io/dag.digest"
		callCPDAttr  = "dagger.io/dag.content_preferred_digest"
		execCPDAttr  = "dagger.io/execution.content_preferred_digest"
		cacheOutcome = "dagger.io/cache.outcome"
	)
	hits := map[string]string{}  // recipe digest -> digest reported by a cache hit
	execs := map[string]string{} // recipe digest -> digest recorded on the execution
	for _, s := range readTelemetrySplitLines[span](ctx, t, cloud, "v1/traces.json.spans") {
		switch {
		case s.Name == "Container.withExec" && s.Attrs[cacheOutcome] == "hit" && s.Attrs[callCPDAttr] != "":
			hits[s.Attrs[recipeAttr]] = s.Attrs[callCPDAttr]
		case s.Name == "exec.run" && s.Attrs[execCPDAttr] != "":
			execs[s.Attrs[recipeAttr]] = s.Attrs[execCPDAttr]
		}
	}
	matched := 0
	for recipe, hit := range hits {
		if exec, ok := execs[recipe]; ok {
			require.Equal(t, hit, exec, "execution and cache hit of recipe %s", recipe)
			matched++
		}
	}
	require.Positive(t, matched, "the second session hit the withExec the first session executed")
}
