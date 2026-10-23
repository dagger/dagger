package dagql

import (
	"context"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

// B's module function call takes its receiver as its own request frame, the
// request-facing call of B's constructor, as a detached receiver's reference
// carries it (resultCallRefFromResult), not the stored frame of the entry its
// constructor hit: so the call's receiver input is B's own constructor digest,
// which differs from A's, as each client's does. The function call still
// hits A's inner result: B's constructor hit taught B's request digest into
// the class of the merged constructor entry, which holds A's, and the
// transferred term matches on input classes, not digests.
func TestTransferredTermsHitThroughARequestFacingReceiver(t *testing.T) {
	t.Parallel()
	content := digest.FromString("terms-test-module-content")
	bundle := termsTestSeed(t, "module-a", content)
	bctx, b, bsrv := transferTestCache(t)
	reply, err := b.MergeValues(bctx, cloudCacheID, bundle)
	require.NoError(t, err)
	require.True(t, reply.Committed)

	mod := termsTestModule(t, bctx, b, bsrv, "module-b", content)
	wolfi, ran := termsTestCall(t, bctx, b, bsrv, "wolfi", nil, mod, termsTestValue("wolfi on B"))
	require.False(t, ran, "B hits A's constructor")

	// B's constructor call, as B requested it.
	request := &ResultCall{
		Kind:   ResultCallKindField,
		Field:  "wolfi",
		Type:   NewResultCallType((&transferTestValue{}).Type()),
		Module: &ResultCallModule{ResultRef: &ResultCallRef{ResultID: uint64(mod.cacheSharedResult().id)}, Name: "terms-test"},
	}
	requested, err := request.deriveRecipeDigest(b)
	require.NoError(t, err)
	stored, err := wolfi.cacheSharedResult().loadResultCall().deriveRecipeDigest(b)
	require.NoError(t, err)
	require.NotEqual(t, stored, requested, "B's receiver digest differs from the seed's")

	frame := &ResultCall{
		Kind:     ResultCallKindField,
		Field:    "container",
		Type:     NewResultCallType((&transferTestValue{}).Type()),
		Receiver: &ResultCallRef{Call: request},
		Module:   &ResultCallModule{ResultRef: &ResultCallRef{ResultID: uint64(mod.cacheSharedResult().id)}, Name: "terms-test"},
	}
	ran = false
	got, err := b.GetOrInitCall(bctx, "test-session", bsrv, &CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (AnyResult, error) {
		ran = true
		return NewResultForCall(&transferTestValue{Text: "container on B"}, frame)
	})
	require.NoError(t, err)
	require.False(t, ran, "B hits A's module function call through its own receiver digest")
	value, ok := got.Unwrap().(*transferTestValue)
	require.True(t, ok)
	require.Equal(t, "inner", value.Text, "the call returns A's inner result")
}
