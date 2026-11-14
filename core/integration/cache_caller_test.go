package core

import (
	"context"
	"crypto/rand"

	"dagger.io/dagger/core"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// A process in an exec with privileged nesting makes its calls under the
// exec's call: each names the withExec's recipe as its caller
// (dagger.io/cache.caller), as a resolver's calls name the resolver's call.
// A module's process started by an API exec, as a module entrypoint starts
// one, is such a process, and the remote cache service orders what it sends
// an engine by these callers.
func (EngineSuite) TestNestedExecCallsNameTheExecAsCaller(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	// The client forwards the session telemetry to the fake Cloud.
	cloud := newTelemetrySplitCloud(t, c)
	engine, err := devEngineContainerAsService(telemetrySplitEngineWithoutCloud(c, devEngineContainer(c))).Start(ctx)
	require.NoError(t, err)

	id := rand.Text()
	args := []string{
		"/bin/dagger", "core", "container",
		"from", "--address=" + alpineImage,
		"with-file", "--path=/bin/dagger", "--source=/bin/dagger",
		"with-env-variable", "--name=CACHEBUSTER", "--value=" + id,
		"with-exec", "--experimental-privileged-nesting",
	}
	for _, arg := range markerExecArgs("nested-caller-", id) {
		args = append(args, "--args="+arg)
	}
	args = append(args, "stdout")
	out, err := telemetrySplitClient(ctx, t, c, daggerCliFile(t, c), engine, cloud).
		WithExec(args, core.ContainerWithExecOpts{DisableDaggerInDagger: true}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "nested-caller-"+id)

	type span struct {
		Name  string            `json:"name"`
		Attrs map[string]string `json:"attrs"`
	}
	const (
		recipeAttr  = "dagger.io/dag.digest"
		callerAttr  = "dagger.io/cache.caller"
		outcomeAttr = "dagger.io/cache.outcome"
	)
	var execs []span
	for _, s := range readTelemetrySplitLines[span](ctx, t, cloud, "v1/traces.json.spans") {
		// The call's own span, which the cache reports, not its profiling
		// twin.
		if s.Name == "Container.withExec" && s.Attrs[outcomeAttr] != "" {
			execs = append(execs, s)
		}
	}
	recipes := map[string]bool{}
	for _, s := range execs {
		recipes[s.Attrs[recipeAttr]] = true
	}
	var nested []span
	for _, s := range execs {
		if recipes[s.Attrs[callerAttr]] {
			nested = append(nested, s)
		}
	}
	require.Len(t, execs, 2, "the session's withExec and the nested CLI's")
	require.Len(t, nested, 1, "the nested CLI's withExec names the session's withExec as its caller: %+v", execs)
}
