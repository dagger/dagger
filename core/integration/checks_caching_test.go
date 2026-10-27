package core

// These tests cover how a check's result is cached: like any other function,
// as its @cache policy says, across sessions and engines. A failure is never
// kept, so a failing check runs again in the next session. Each check's body
// records its runs, so the tests count how often it really ran.
//
// See also:
// - checks_test.go: running and listing checks.
// - engine_persistence_test.go: a cached check across an engine restart.

import (
	"context"
	"crypto/rand"
	"strings"

	"dagger.io/dagger/core"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// checkCachingModule is the fixture module at dir, made unique to this test,
// so neither its checks' results nor its record of runs are shared with
// another test.
func checkCachingModule(env *core.Container, dir string) *core.Container {
	return env.
		WithWorkdir(dir).
		WithNewFile("nonce.go", "package main\n\nconst nonce = \""+rand.Text()+"\"\n")
}

// checkCachingRun runs the dagger CLI in a new session, so only the cache
// connects one run to the next.
func checkCachingRun(ctx context.Context, t *testctx.T, mod *core.Container, expect core.ReturnType, args ...string) string {
	t.Helper()
	out, err := mod.
		WithEnvVariable("CACHE_BUST", rand.Text()).
		WithExec(append([]string{"dagger", "--progress=report"}, args...), core.ContainerWithExecOpts{
			Expect:                   expect,
			DaggerInDaggerNewSession: true,
		}).
		CombinedOutput(ctx)
	require.NoError(t, err)
	return out
}

// checkCachingRuns returns how often the body of check name has run.
func checkCachingRuns(ctx context.Context, t *testctx.T, mod *core.Container, args ...string) string {
	t.Helper()
	out, err := mod.
		WithEnvVariable("CACHE_BUST", rand.Text()).
		WithExec(append([]string{"dagger", "call", "runs"}, args...), core.ContainerWithExecOpts{
			DaggerInDaggerNewSession: true,
		}).
		Stdout(ctx)
	require.NoError(t, err)
	return strings.TrimSpace(out)
}

func (ChecksSuite) TestChecksCachedAcrossSessions(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	env, err := checksTestEnv(t, c)
	require.NoError(t, err)

	t.Run("a passing check is reused in a new session", func(ctx context.Context, t *testctx.T) {
		mod := checkCachingModule(env, "check-caching")
		checkCachingRun(ctx, t, mod, core.ReturnTypeSuccess, "check", "pass")
		checkCachingRun(ctx, t, mod, core.ReturnTypeSuccess, "check", "pass")
		require.Equal(t, "1", checkCachingRuns(ctx, t, mod, "--name", "pass"))
	})

	t.Run("a failing check runs again in the next session", func(ctx context.Context, t *testctx.T) {
		mod := checkCachingModule(env, "check-caching")
		checkCachingRun(ctx, t, mod, core.ReturnTypeFailure, "check", "fail")
		checkCachingRun(ctx, t, mod, core.ReturnTypeFailure, "check", "fail")
		require.Equal(t, "2", checkCachingRuns(ctx, t, mod, "--name", "fail"))
	})

	// Asking for the same check twice in one session runs it once, unless it
	// is never cached.
	t.Run("within a session", func(ctx context.Context, t *testctx.T) {
		for _, tc := range []struct {
			check string
			want  string
		}{
			{"fail", "1"},
			{"per-session", "1"},
			{"never", "2"},
		} {
			t.Run(tc.check, func(ctx context.Context, t *testctx.T) {
				mod := checkCachingModule(env, "check-caching")
				checkCachingRun(ctx, t, mod, core.ReturnTypeAny, "-c", tc.check+" | pass; "+tc.check+" | pass")
				require.Equal(t, tc.want, checkCachingRuns(ctx, t, mod, "--name", tc.check))
			})
		}
	})

	t.Run("a per-session check runs once in each session", func(ctx context.Context, t *testctx.T) {
		mod := checkCachingModule(env, "check-caching")
		checkCachingRun(ctx, t, mod, core.ReturnTypeSuccess, "check", "per-session")
		checkCachingRun(ctx, t, mod, core.ReturnTypeSuccess, "check", "per-session")
		require.Equal(t, "2", checkCachingRuns(ctx, t, mod, "--name", "per-session"))
	})

	// A check from a module written before v1.0 may return a container,
	// which the check then runs. When the container fails, the next session
	// runs the check's body again, not only the container.
	t.Run("a failing returned container reruns the check's body", func(ctx context.Context, t *testctx.T) {
		mod := checkCachingModule(env, "check-caching-legacy")
		checkCachingRun(ctx, t, mod, core.ReturnTypeFailure, "check", "failing-container")
		checkCachingRun(ctx, t, mod, core.ReturnTypeFailure, "check", "failing-container")
		require.Equal(t, "2", checkCachingRuns(ctx, t, mod))
	})
}
