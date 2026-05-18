// These volatile-variable tests live in the container integration suite because
// the behavior under test crosses GraphQL recipe cache lookup and container exec
// materialization. Unit tests for env helpers or call digests cannot catch stale
// volatile values being returned inside cached Container results.
package core

import (
	"context"

	"github.com/stretchr/testify/require"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
)

// TestVolatileVariableCachedExecOutputSeesLatestValue verifies that a cache hit
// for an exec whose key intentionally ignores volatile values does not also
// reuse the cached output container's stale volatile environment. The first exec
// is a cacheable no-op that should be shared across RUN_ID changes; the second
// exec changes an ordinary input so it must rerun and read RUN_ID from the
// cached parent. If the engine returns the first cached output unchanged, the
// second run observes "one:second" instead of the current value "two:second".
func (ContainerSuite) TestVolatileVariableCachedExecOutputSeesLatestValue(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := c.Container().From(alpineImage)

	run := func(runID, marker string) string {
		out, err := base.
			WithVolatileVariable("RUN_ID", runID).
			WithExec([]string{"true"}).
			WithExec([]string{"sh", "-c", `printf '%s:%s' "$RUN_ID" "$1"`, "_", marker}).
			Stdout(ctx)
		require.NoError(t, err)
		return out
	}

	require.Equal(t, "one:first", run("one", "first"))
	require.Equal(t, "two:second", run("two", "second"))
}

// TestVolatileVariableCacheHitKeepsEachContainerValue verifies that rebasing a
// volatile exec cache hit is scoped to the requesting Container result. Volatile
// values are intentionally ignored for the first no-op exec's broad cache key,
// but the returned Container is still an immutable value used by later execs and
// by serialized IDs. A cache-hit rebase that mutates the shared cached result
// would make the earlier RUN_ID=one handle, or its ID, observe RUN_ID=two after
// the second equivalent no-op exec.
func (ContainerSuite) TestVolatileVariableCacheHitKeepsEachContainerValue(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := c.Container().From(alpineImage)

	read := func(ctr *dagger.Container, marker string) string {
		out, err := ctr.
			WithExec([]string{"sh", "-c", `printf '%s:%s' "$RUN_ID" "$1"`, "_", marker}).
			Stdout(ctx)
		require.NoError(t, err)
		return out
	}

	first := base.
		WithVolatileVariable("RUN_ID", "one").
		WithExec([]string{"true"})
	require.Equal(t, "one:first", read(first, "first"))
	firstID, err := first.ID(ctx)
	require.NoError(t, err)

	second := base.
		WithVolatileVariable("RUN_ID", "two").
		WithExec([]string{"true"})
	require.Equal(t, "two:second", read(second, "second"))
	secondID, err := second.ID(ctx)
	require.NoError(t, err)

	require.Equal(t, "one:first-again", read(first, "first-again"))
	require.Equal(t, "one:first-id", read(c.LoadContainerFromID(firstID), "first-id"))
	require.Equal(t, "two:second-id", read(c.LoadContainerFromID(secondID), "second-id"))
}
