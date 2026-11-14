package dagql

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/call"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

// contentPreferredTestBinary is a file an engine builds, F: a result with no
// content digest until its engine teaches it one.
func contentPreferredTestBinary(t *testing.T, ctx context.Context, c *Cache, srv *Server, field string) AnyResult {
	t.Helper()
	return persistedListTestResult(t, ctx, c, srv, field, &transferTestValue{Text: "binary"})
}

// contentPreferredTestService is a service over a file, S: a call whose
// receiver is the file's entry, as asService's frame reaches the CLI binary.
func contentPreferredTestService(t *testing.T, ctx context.Context, c *Cache, srv *Server, file AnyResult) AnyResult {
	t.Helper()
	c.egraphMu.RLock()
	fileID := uint64(file.cacheSharedResult().id)
	c.egraphMu.RUnlock()
	frame := callRecordTestFrame("service")
	frame.Receiver = &ResultCallRef{ResultID: fileID}
	res, err := c.GetOrInitCall(ctx, "test-session", srv, &CallRequest{ResultCall: frame, IsPersistable: true},
		func(context.Context) (AnyResult, error) {
			return NewResultForCall(&transferTestValue{Text: "service"}, frame)
		})
	require.NoError(t, err)
	return res
}

// contentPreferredTestDigest is the content-preferred digest of the entry's
// frame, from which core derives a service's hostname
// (core.Service.Hostname).
func contentPreferredTestDigest(t *testing.T, c *Cache, res *sharedResult) digest.Digest {
	t.Helper()
	frame := res.loadResultCall()
	require.NotNil(t, frame, "entry %d has a decoded frame", res.id)
	dig, err := frame.deriveContentPreferredDigest(c)
	require.NoError(t, err)
	return dig
}

// contentPreferredTestRecipe is the recipe digest of res's frame.
func contentPreferredTestRecipe(t *testing.T, c *Cache, res AnyResult) digest.Digest {
	t.Helper()
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	return res.cacheSharedResult().recipeKeys[0]
}

// A fresh engine that takes a service's record from the Cloud gives the
// service the content-preferred digest, and so the hostname, its producer
// gave it, as a warm engine does from the memo on its own entry: although
// the fresh engine built the service's file itself first and taught it a
// content digest the producer never knew, so a walk of the service's frame
// over its own refs comes out another way. ne6's docs-only edit: the CLI
// binary's content made the test engine's hostname new, and the test run
// below it ran again.
func TestFreshEngineGivesAServiceTheContentPreferredDigestItsProducerUsed(t *testing.T) {
	t.Parallel()
	actx, a, asrv := transferTestCache(t)
	file := contentPreferredTestBinary(t, actx, a, asrv, "binary")
	service := contentPreferredTestService(t, actx, a, asrv, file)
	recipe := contentPreferredTestRecipe(t, a, service)
	produced := contentPreferredTestDigest(t, a, service.cacheSharedResult())

	cloudCtx, cloud := storedPartTestCache(t, WithBlobStore())
	numbers := storedPersistenceTestMerge(t, cloudCtx, cloud, exportTestBundle(t, actx, a, service))
	require.Len(t, numbers, 1)
	sent, err := cloud.StoredBundle(cloudCtx, numbers, time.Now().Unix(), nil)
	require.NoError(t, err)

	bctx, b, bsrv := transferTestCache(t)
	own := contentPreferredTestBinary(t, bctx, b, bsrv, "binary")
	require.NoError(t, b.TeachContentDigest(bctx, own, digest.FromString("binary content")))
	_, err = b.MergeValues(bctx, cloudCacheID, sent.Bundle)
	require.NoError(t, err)
	installed := b.EquivalentEntries(recipe.String())
	require.Len(t, installed, 1, "b holds the Cloud's service")
	b.egraphMu.RLock()
	received := b.resultsByID[sharedResultID(installed[0])]
	b.egraphMu.RUnlock()
	require.Equal(t, produced, contentPreferredTestDigest(t, b, received), "b names the service as its producer did")
}

// An engine gives a service the content-preferred digest it gave it before
// its store was saved and restored, though the service's file learned its
// content digest in between.
func TestContentPreferredDigestSurvivesARestore(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "cache.db")
	ctx, c, srv := contentPreferredTestCacheAt(t, path)
	file := contentPreferredTestBinary(t, ctx, c, srv, "binary")
	service := contentPreferredTestService(t, ctx, c, srv, file)
	recipe := contentPreferredTestRecipe(t, c, service)
	before := contentPreferredTestDigest(t, c, service.cacheSharedResult())
	require.NoError(t, c.TeachContentDigest(ctx, file, digest.FromString("binary content")))
	require.NoError(t, c.Close(ctx))

	_, c, _ = contentPreferredTestCacheAt(t, path)
	defer func() { require.NoError(t, c.CloseDiscardingPersistence()) }()
	restored := c.EquivalentEntries(recipe.String())
	require.Len(t, restored, 1, "the restore keeps the service")
	c.egraphMu.RLock()
	res := c.resultsByID[sharedResultID(restored[0])]
	c.egraphMu.RUnlock()
	require.Equal(t, before, contentPreferredTestDigest(t, c, res), "the restored service keeps its name")
}

// contentPreferredTestCacheAt opens an engine cache saved at path, which the
// caller closes.
func contentPreferredTestCacheAt(t *testing.T, path string) (context.Context, *Cache, *Server) {
	t.Helper()
	ctx := cacheTestContext(t.Context())
	c, err := NewCache(ctx, path, nil, nil)
	require.NoError(t, err)
	srv := newDagqlServerForTest(t, &persistCodecRoot{})
	srv.InstallObject(NewClass(srv, ClassOpts[*transferTestValue]{}))
	return srvToContext(ContextWithCache(ctx, c), srv), c, srv
}

// A clone of a frame carries its content-preferred digest, and a clone
// changed into another recipe, as a list element's frame or a derived call
// is, doesn't use it.
func TestACloneThatChangesTheRecipeIgnoresTheCarriedContentPreferredDigest(t *testing.T) {
	t.Parallel()
	ctx, c, srv := transferTestCache(t)
	file := contentPreferredTestBinary(t, ctx, c, srv, "binary")
	service := contentPreferredTestService(t, ctx, c, srv, file)
	frame := service.cacheSharedResult().loadResultCall()
	produced := contentPreferredTestDigest(t, c, service.cacheSharedResult())

	same := frame.clone()
	require.NotNil(t, same.ContentPreferred, "a clone carries the computed value")
	require.Equal(t, produced, same.ContentPreferred.Digest)
	got, err := same.deriveContentPreferredDigest(c)
	require.NoError(t, err)
	require.Equal(t, produced, got)

	changed := frame.clone()
	changed.Field = "other-service"
	got, err = changed.deriveContentPreferredDigest(c)
	require.NoError(t, err)
	fresh := callRecordTestFrame("other-service")
	fresh.Receiver = &ResultCallRef{ResultID: frame.Receiver.ResultID}
	want, err := fresh.deriveContentPreferredDigest(c)
	require.NoError(t, err)
	require.Equal(t, want, got, "a changed clone walks its own frame")
	require.NotEqual(t, produced, got)
}

// A frame that carries its producer's content-preferred digest and then
// gains a content digest of its own has that content digest as its
// content-preferred digest, as a frame computed locally does.
func TestAContentDigestOverridesTheCarriedContentPreferredDigest(t *testing.T) {
	t.Parallel()
	ctx, c, srv := transferTestCache(t)
	file := contentPreferredTestBinary(t, ctx, c, srv, "binary")
	service := contentPreferredTestService(t, ctx, c, srv, file)
	contentPreferredTestDigest(t, c, service.cacheSharedResult())
	carried := service.cacheSharedResult().loadResultCall().clone()
	require.NotNil(t, carried.ContentPreferred)

	content := digest.FromString("service content")
	require.NoError(t, c.TeachContentDigest(ctx, service, content))
	require.Equal(t, content, contentPreferredTestDigest(t, c, service.cacheSharedResult()))
	require.Nil(t, service.cacheSharedResult().loadResultCall().ContentPreferred, "the taught frame is a fork, which carries none")

	carried.ExtraDigests = append(carried.ExtraDigests, call.ExtraDigest{Digest: content, Label: call.ExtraDigestLabelContent})
	got, err := carried.deriveContentPreferredDigest(c)
	require.NoError(t, err)
	require.Equal(t, content, got, "a frame's own content digest comes first")
}
