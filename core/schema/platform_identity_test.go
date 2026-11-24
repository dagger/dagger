package schema

import (
	"context"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

var declaredSourceContent = digest.FromString("declared-source-content")

// declaredSource is directory().withNewFile("a", "declared").
func declaredSource(t *testing.T, e *transferTestEngine, ctx context.Context) dagql.ObjectResult[*core.Directory] {
	t.Helper()
	var src dagql.ObjectResult[*core.Directory]
	require.NoError(t, e.dag.Select(ctx, e.dag.Root(), &src,
		dagql.Selector{Field: "directory"},
		dagql.Selector{Field: "withNewFile", Args: []dagql.NamedInput{
			{Name: "path", Value: dagql.NewString("a")},
			{Name: "contents", Value: dagql.NewString("declared")},
		}}))
	return src
}

// declareForArm64 is directory().withDirectory("/", src)
// .withPlatform("linux/arm64").
func declareForArm64(t *testing.T, e *transferTestEngine, ctx context.Context, src dagql.ObjectResult[*core.Directory]) dagql.ObjectResult[*core.Directory] {
	t.Helper()
	srcID, err := src.ID()
	require.NoError(t, err)
	var declared dagql.ObjectResult[*core.Directory]
	require.NoError(t, e.dag.Select(ctx, e.dag.Root(), &declared,
		dagql.Selector{Field: "directory"},
		dagql.Selector{Field: "withDirectory", Args: []dagql.NamedInput{
			{Name: "path", Value: dagql.NewString("/")},
			{Name: "source", Value: dagql.NewID[*core.Directory](srcID)},
		}},
		dagql.Selector{Field: "withPlatform", Args: []dagql.NamedInput{
			{Name: "platform", Value: core.Platform{OS: "linux", Architecture: "arm64"}},
		}}))
	return declared
}

// A result declared as made for a platform is declared on an amd64 engine
// before an input learns its content, as an engine image's build declares a
// tool built from a git tree it hasn't fetched yet. An arm64 engine that
// merges it declares the same result after that input's content is known, as
// the input arrives with the merge. Its lookup finds the merged result: once
// evaluated, the declaration also took the identity the input's content
// gives it.
func TestDeclaredPlatformResultHitsAfterItsInputLearnsContent(t *testing.T) {
	a := newTransferTestEngine(t, transferTestSalt(t))
	b := newTransferTestEngine(t, transferTestSalt(t))
	b.srv.platform = core.Platform{OS: "linux", Architecture: "arm64"}

	actx := a.session(t, "a1")
	src := declaredSource(t, a, actx)
	declared := declareForArm64(t, a, actx, src)
	require.False(t, declared.HitCache(), "the first declaration runs")
	// The input learns its content after the declaration, as a git tree
	// does when it is evaluated.
	require.NoError(t, a.cache.TeachContentDigest(actx, src, declaredSourceContent))
	require.NoError(t, a.cache.Evaluate(actx, declared))

	bundle := a.export(t, actx, declared)
	bctx := b.session(t, "b1")
	reply, err := b.cache.MergeValues(bctx, dagql.CloudCacheID, bundle)
	require.NoError(t, err)
	require.Len(t, reply.Imported(), 1, "the declared result lands on the other engine")

	// On the arm64 engine, every recipe that takes the engine's platform
	// differs, so only the declared identity can find the merged result.
	srcB := declaredSource(t, b, bctx)
	require.NoError(t, b.cache.TeachContentDigest(bctx, srcB, declaredSourceContent))
	onB := declareForArm64(t, b, bctx, srcB)
	require.True(t, onB.HitCache(), "the declaration made after its input learned its content finds the merged result")
}
