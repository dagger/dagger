package dagql

import (
	"context"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

// termsTestCall calls field in the cache, with the receiver and the module
// provenance given, if any, as a module function call frames them. init
// computes the value on a miss; termsTestCall reports whether it ran.
func termsTestCall(t *testing.T, ctx context.Context, c *Cache, srv *Server, field string, receiver, module AnyResult, init func(*ResultCall) (AnyResult, error)) (AnyResult, bool) {
	t.Helper()
	frame := &ResultCall{Kind: ResultCallKindField, Field: field, Type: NewResultCallType((&transferTestValue{}).Type())}
	if receiver != nil {
		frame.Receiver = &ResultCallRef{ResultID: uint64(receiver.cacheSharedResult().id)}
	}
	if module != nil {
		frame.Module = &ResultCallModule{ResultRef: &ResultCallRef{ResultID: uint64(module.cacheSharedResult().id)}, Name: "terms-test"}
	}
	ran := false
	res, err := c.GetOrInitCall(ctx, "test-session", srv, &CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (AnyResult, error) {
		ran = true
		return init(frame)
	})
	require.NoError(t, err)
	return res, ran
}

// termsTestValue is an init that computes a fresh value for the frame.
func termsTestValue(text string) func(*ResultCall) (AnyResult, error) {
	return func(frame *ResultCall) (AnyResult, error) {
		return NewResultForCall(&transferTestValue{Text: text}, frame)
	}
}

// termsTestModule computes a module result of field, with the content
// digest content: a module's implementation, whose recipe differs per client
// and whose content digest doesn't.
func termsTestModule(t *testing.T, ctx context.Context, c *Cache, srv *Server, field string, content digest.Digest) AnyResult {
	t.Helper()
	mod, _ := termsTestCall(t, ctx, c, srv, field, nil, nil, termsTestValue("module"))
	require.NoError(t, c.TeachContentDigest(ctx, mod, content))
	return mod
}

// termsTestSeed is engine A's run, which a warm engine hits the function
// call of: its module, the constructor wolfi with the module's provenance,
// an inner result, and the module function wolfi.container, which returns
// that inner result. It exports the inner result, the constructor and the
// module to a Cloud cache, and returns the Cloud's stored bundle of them.
func termsTestSeed(t *testing.T, moduleField string, content digest.Digest) ValueBundle {
	t.Helper()
	actx, a, asrv := transferTestCache(t)
	mod := termsTestModule(t, actx, a, asrv, moduleField, content)
	wolfi, _ := termsTestCall(t, actx, a, asrv, "wolfi", nil, mod, termsTestValue("wolfi"))
	inner, _ := termsTestCall(t, actx, a, asrv, "withEnvVariable", nil, nil, termsTestValue("inner"))
	got, ran := termsTestCall(t, actx, a, asrv, "container", wolfi, mod, func(*ResultCall) (AnyResult, error) { return inner, nil })
	require.True(t, ran)
	require.Equal(t, inner.cacheSharedResult().id, got.cacheSharedResult().id, "the function returns the inner result")
	again, ran := termsTestCall(t, actx, a, asrv, "container", wolfi, mod, termsTestValue("recomputed"))
	require.False(t, ran, "a warm engine hits the function call")
	require.Equal(t, inner.cacheSharedResult().id, again.cacheSharedResult().id)

	bundle := exportTestBundle(t, actx, a, inner, wolfi, mod)
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	reply, err := cloud.MergeValues(ctx, "cache-a", bundle)
	require.NoError(t, err)
	var numbers []uint64
	for _, value := range reply.Values {
		numbers = append(numbers, value.Number)
	}
	stored, err := cloud.StoredBundle(ctx, numbers, nil)
	require.NoError(t, err)
	require.Empty(t, stored.Gone)
	require.Empty(t, stored.NoValue)
	return stored.Bundle
}

// A fresh engine B that merges the Cloud's bundle of A's run, and hits A's
// constructor, hits A's module function call too, as a warm engine does: the
// call returns A's inner result, which carries the call's term.
//   - exact: B's module has A's recipe, so B's calls have A's request
//     digests.
//   - per client: B's module has a recipe of its own, as each client's
//     module provenance does, and A's content digest; B's constructor hits
//     by its term, and the function call can only hit by its term too.
func TestTransferredTermsHitTheFunctionCallOnB(t *testing.T) {
	t.Parallel()
	content := digest.FromString("terms-test-module-content")
	for name, bModuleField := range map[string]string{"exact": "module-a", "per client": "module-b"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			bundle := termsTestSeed(t, "module-a", content)
			bctx, b, bsrv := transferTestCache(t)
			reply, err := b.MergeValues(bctx, cloudCacheID, bundle)
			require.NoError(t, err)
			require.True(t, reply.Committed)

			mod := termsTestModule(t, bctx, b, bsrv, bModuleField, content)
			wolfi, ran := termsTestCall(t, bctx, b, bsrv, "wolfi", nil, mod, termsTestValue("wolfi on B"))
			require.False(t, ran, "B hits A's constructor")
			got, ran := termsTestCall(t, bctx, b, bsrv, "container", wolfi, mod, termsTestValue("container on B"))
			require.False(t, ran, "B hits A's module function call")
			value, ok := got.Unwrap().(*transferTestValue)
			require.True(t, ok)
			require.Equal(t, "inner", value.Text, "the call returns A's inner result")
		})
	}
}

// A term whose input class B doesn't hold gives B no false hit: B's module
// shares nothing with A's, so B computes its constructor, and its function
// call misses, as an ordinary miss, and runs.
func TestTransferredTermsGiveNoFalseHit(t *testing.T) {
	t.Parallel()
	bundle := termsTestSeed(t, "module-a", digest.FromString("terms-test-module-content"))
	bctx, b, bsrv := transferTestCache(t)
	reply, err := b.MergeValues(bctx, cloudCacheID, bundle)
	require.NoError(t, err)
	require.True(t, reply.Committed)

	mod := termsTestModule(t, bctx, b, bsrv, "module-b", digest.FromString("terms-test-other-content"))
	wolfi, ran := termsTestCall(t, bctx, b, bsrv, "wolfi", nil, mod, termsTestValue("wolfi on B"))
	require.True(t, ran, "B's constructor of another module computes")
	got, ran := termsTestCall(t, bctx, b, bsrv, "container", wolfi, mod, termsTestValue("container on B"))
	require.True(t, ran, "B's function call misses, and runs")
	value, ok := got.Unwrap().(*transferTestValue)
	require.True(t, ok)
	require.Equal(t, "container on B", value.Text)
}
