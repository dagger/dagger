package dagql

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
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

// termsTestStripped returns the bundle with no transfer terms on any record,
// as a cache that doesn't know them encodes the records it decoded.
func termsTestStripped(t *testing.T, bundle ValueBundle) ValueBundle {
	t.Helper()
	out, err := cloneValueBundle(bundle)
	require.NoError(t, err)
	for i := range out.Values {
		if call := out.Values[i].Record.Call; call != nil {
			call = call.clone()
			call.TransferTerms = nil
			out.Values[i].Record.Call = call
		}
	}
	return out
}

// termsTestTerms returns the transfer terms the bundle's records carry.
func termsTestTerms(bundle ValueBundle) []TransferTerm {
	var terms []TransferTerm
	for _, value := range bundle.Values {
		if value.Record.Call != nil {
			terms = append(terms, value.Record.Call.TransferTerms...)
		}
	}
	return terms
}

// The Cloud's bundle of A's run carries the function call's term in the
// inner result's record, over the call's inputs as A derived them, with
// module provenance among them.
func TestTransferredTermsRideInTheRecord(t *testing.T) {
	t.Parallel()
	bundle := termsTestSeed(t, "module-a", digest.FromString("terms-test-module-content"))
	terms := termsTestTerms(bundle)
	require.Len(t, terms, 1, "the function call's term, on the inner result")
	require.NotEmpty(t, terms[0].Request)
	require.NotEmpty(t, terms[0].Self)
	require.Len(t, terms[0].Inputs, 2, "the receiver and the module")
	require.Len(t, terms[0].Provenance, 2)
}

// Engines and services of every mix exchange bundles as before: none refuses
// a bundle for the terms, or for their absence, and each behaves as today
// where they are missing.
//   - an old engine: it decodes the records without the field, as a bundle
//     with the terms stripped, and merges; it hits the constructor and runs
//     the function call, as today.
//   - an old service: it stores what it decoded, without the terms; its
//     stored bundle carries none, and a new engine merges it as today.
//   - the decoding: an unknown key in a record's call, as an old engine
//     meets "transferTerms", decodes without error.
func TestTransferredTermsMixedVersions(t *testing.T) {
	t.Parallel()
	content := digest.FromString("terms-test-module-content")
	t.Run("old engine", func(t *testing.T) {
		t.Parallel()
		bundle := termsTestStripped(t, termsTestSeed(t, "module-a", content))
		bctx, b, bsrv := transferTestCache(t)
		reply, err := b.MergeValues(bctx, cloudCacheID, bundle)
		require.NoError(t, err)
		require.True(t, reply.Committed)
		mod := termsTestModule(t, bctx, b, bsrv, "module-b", content)
		wolfi, ran := termsTestCall(t, bctx, b, bsrv, "wolfi", nil, mod, termsTestValue("wolfi on B"))
		require.False(t, ran, "the constructor hits, as today")
		_, ran = termsTestCall(t, bctx, b, bsrv, "container", wolfi, mod, termsTestValue("container on B"))
		require.True(t, ran, "the function call runs, as today")
	})
	t.Run("old service", func(t *testing.T) {
		t.Parallel()
		actx, a, asrv := transferTestCache(t)
		mod := termsTestModule(t, actx, a, asrv, "module-a", content)
		wolfi, _ := termsTestCall(t, actx, a, asrv, "wolfi", nil, mod, termsTestValue("wolfi"))
		inner, _ := termsTestCall(t, actx, a, asrv, "withEnvVariable", nil, nil, termsTestValue("inner"))
		termsTestCall(t, actx, a, asrv, "container", wolfi, mod, func(*ResultCall) (AnyResult, error) { return inner, nil })
		exported := exportTestBundle(t, actx, a, inner, wolfi, mod)
		require.NotEmpty(t, termsTestTerms(exported), "the new engine sends the term")

		ctx, cloud := storedPartTestCache(t, WithBlobStore())
		reply, err := cloud.MergeValues(ctx, "cache-a", termsTestStripped(t, exported))
		require.NoError(t, err, "a service that drops the terms merges the export")
		var numbers []uint64
		for _, value := range reply.Values {
			numbers = append(numbers, value.Number)
		}
		stored, err := cloud.StoredBundle(ctx, numbers, nil)
		require.NoError(t, err)
		require.Empty(t, termsTestTerms(stored.Bundle), "and stores none")

		bctx, b, bsrv := transferTestCache(t)
		merged, err := b.MergeValues(bctx, cloudCacheID, stored.Bundle)
		require.NoError(t, err, "a new engine merges a bundle without terms")
		require.True(t, merged.Committed)
		bmod := termsTestModule(t, bctx, b, bsrv, "module-b", content)
		bwolfi, ran := termsTestCall(t, bctx, b, bsrv, "wolfi", nil, bmod, termsTestValue("wolfi on B"))
		require.False(t, ran)
		_, ran = termsTestCall(t, bctx, b, bsrv, "container", bwolfi, bmod, termsTestValue("container on B"))
		require.True(t, ran, "the function call runs, as today")
	})
	t.Run("decoding", func(t *testing.T) {
		t.Parallel()
		var call ResultCall
		require.NoError(t, json.Unmarshal([]byte(`{"kind":"field","field":"f","someFutureField":[{"x":1}]}`), &call))
		require.Equal(t, "f", call.Field)
	})
}

// A result's transfer terms persist with its frame: after its engine
// restarts, the inner result still carries the function call's term, and its
// export sends it.
func TestTransferredTermsPersist(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "cache.db")
	ctx, a, srv := persistedListTestCache(t, path)
	srv.InstallObject(NewClass(srv, ClassOpts[*transferTestValue]{}))
	mod := termsTestModule(t, ctx, a, srv, "module-a", digest.FromString("terms-test-module-content"))
	wolfi, _ := termsTestCall(t, ctx, a, srv, "wolfi", nil, mod, termsTestValue("wolfi"))
	inner, _ := termsTestCall(t, ctx, a, srv, "withEnvVariable", nil, nil, termsTestValue("inner"))
	termsTestCall(t, ctx, a, srv, "container", wolfi, mod, func(*ResultCall) (AnyResult, error) { return inner, nil })
	innerID, err := a.PersistedResultID(inner)
	require.NoError(t, err)
	require.Len(t, inner.cacheSharedResult().loadResultCall().TransferTerms, 1)
	require.NoError(t, a.ReleaseSession(ctx, "test-session"))
	require.NoError(t, a.Close(ctx))

	ctx, a, srv = persistedListTestCache(t, path)
	srv.InstallObject(NewClass(srv, ClassOpts[*transferTestValue]{}))
	restored, err := a.LoadResultByResultID(ctx, "test-session", srv, innerID)
	require.NoError(t, err)
	require.Len(t, restored.cacheSharedResult().loadResultCall().TransferTerms, 1, "the term survives the restart")
	require.Len(t, termsTestTerms(exportTestBundle(t, ctx, a, restored)), 1, "and the export sends it")
}

// A frame keeps the first maxTransferTerms terms; another is dropped, and the
// frame is left as it was.
func TestTransferredTermsCapped(t *testing.T) {
	t.Parallel()
	ctx, c, srv := transferTestCache(t)
	res, _ := termsTestCall(t, ctx, c, srv, "capped", nil, nil, termsTestValue("capped"))
	shared := res.cacheSharedResult()
	c.egraphMu.Lock()
	for i := range maxTransferTerms + 1 {
		c.addTransferTermLocked(ctx, shared, TransferTerm{Request: digest.FromString(fmt.Sprint("request ", i)), Self: digest.FromString("self")})
	}
	c.egraphMu.Unlock()
	terms := shared.loadResultCall().TransferTerms
	require.Len(t, terms, maxTransferTerms)
	require.Equal(t, digest.FromString("request 0"), terms[0].Request, "the first ones are kept")
}

// A frame derived for another result carries none of the transfer terms of
// the frame it was derived from: they name that result, and a list's terms
// on its elements would teach each element the whole list's identity. Here a
// list result's frame has a term, and its elements' frames, read through
// NthValue as the server builds a list response, and a persisted list item's
// frame, have none; neither has a plain copy of the frame, which callers
// reshape into frames of other calls.
func TestTransferredTermsStayOffDerivedFrames(t *testing.T) {
	t.Parallel()
	ctx, c, srv := transferTestCache(t)
	frame := &ResultCall{Kind: ResultCallKindField, Field: "keys", Type: NewResultCallType(NewStringArray().Type())}
	list, err := c.GetOrInitCall(ctx, "test-session", srv, &CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (AnyResult, error) {
		return NewResultForCall(NewStringArray("a", "b"), frame)
	})
	require.NoError(t, err)
	shared := list.cacheSharedResult()
	c.egraphMu.Lock()
	c.addTransferTermLocked(ctx, shared, TransferTerm{Request: digest.FromString("a list call"), Self: digest.FromString("self")})
	c.egraphMu.Unlock()
	listFrame := shared.loadResultCall()
	require.Len(t, listFrame.TransferTerms, 1, "the list's frame has the term")

	for nth := 1; nth <= 2; nth++ {
		item, err := list.NthValue(ctx, nth)
		require.NoError(t, err)
		itemFrame := item.cacheSharedResult().loadResultCall()
		require.NotNil(t, itemFrame)
		require.Empty(t, itemFrame.TransferTerms, "element %d", nth)
	}
	require.Empty(t, persistedListItemCall(listFrame, 1).TransferTerms, "a persisted list item")
	require.Empty(t, listFrame.clone().TransferTerms, "a plain copy")
	require.Empty(t, listFrame.fork().TransferTerms, "a plain fork")
}
