package dagql

import (
	"context"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

func TestValueTransferFixtureExactRoots(t *testing.T) {
	ctx, c, srv := transferTestCache(t)
	lower := persistedListTestResult(t, ctx, c, srv, "lower", String("lower"))
	exact := persistedListTestResult(t, ctx, c, srv, "exact", String("exact"))
	for _, value := range []AnyResult{lower, exact} {
		_, err := value.WithContentDigestAny(ctx, digest.FromString("same"), call.ExtraDigestLabelRemoteCache)
		require.NoError(t, err)
	}
	id, err := exact.ID()
	require.NoError(t, err)
	shared := exact.cacheSharedResult()
	before := shared.incomingOwnershipCount
	require.NoError(t, c.WithTransferFixtureRoots(ctx, "test-session", []*call.ID{id}, func(roots []AnyResult) error {
		require.Same(t, shared, roots[0].cacheSharedResult())
		require.Equal(t, before+1, shared.incomingOwnershipCount)
		require.NoError(t, c.WithTransferFixtureRoots(ctx, "test-session", []*call.ID{id}, func([]AnyResult) error { return nil }))
		return nil
	}))
	require.Equal(t, before, shared.incomingOwnershipCount)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	require.ErrorIs(t, c.WithTransferFixtureRoots(canceled, "test-session", []*call.ID{id}, func([]AnyResult) error { t.Fatal("canceled consumer"); return nil }), context.Canceled)
	require.Equal(t, before, shared.incomingOwnershipCount)
	require.Error(t, c.WithTransferFixtureRoots(ctx, "test-session", []*call.ID{call.NewEngineResultID(id.EngineResultID(), call.NewType(Int(0).Type()))}, func([]AnyResult) error { return nil }))
	report, err := c.TransferFixtureSnapshot(ctx, "test-session", []*call.ID{id})
	require.NoError(t, err)
	require.Len(t, report.Rows, 1)
	require.Equal(t, id.EngineResultID(), report.Rows[0].ResultID)
	require.Equal(t, before, shared.incomingOwnershipCount)
}

func TestSchemaModuleSelectionFallback(t *testing.T) {
	ctx, c, srv := transferTestCache(t)
	lower := persistedListTestResult(t, ctx, c, srv, "foreign", String("foreign"))
	lower.cacheSharedResult().imported = true
	exact := persistedListTestResult(t, ctx, c, srv, "native", String("native"))
	for _, value := range []AnyResult{lower, exact} {
		_, err := value.WithContentDigestAny(ctx, digest.FromString("implementation"), call.ExtraDigestLabelRemoteCache)
		require.NoError(t, err)
	}
	recorded := uint64(exact.cacheSharedResult().id)
	got, err := c.LoadResultByResultIDForSchema(ctx, "test-session", srv, recorded, nil)
	require.NoError(t, err)
	require.Same(t, exact.cacheSharedResult(), got.cacheSharedResult())
	c.egraphMu.Lock()
	exact.cacheSharedResult().expiresAtUnix = time.Now().Add(-time.Hour).Unix()
	c.egraphMu.Unlock()
	got, err = c.LoadResultByResultIDForSchema(ctx, "test-session", srv, recorded, nil)
	require.NoError(t, err)
	require.Same(t, lower.cacheSharedResult(), got.cacheSharedResult())
	// An installed Module outlives expiry of the ordinary cache entry.
	got, err = c.LoadResultByResultIDForSchema(ctx, "test-session", srv, uint64(lower.cacheSharedResult().id), []SchemaModuleCandidate{{ModuleResultID: recorded, ScopedResultID: recorded}})
	require.NoError(t, err)
	require.Same(t, exact.cacheSharedResult(), got.cacheSharedResult())
	_, err = c.LoadResultByResultIDForSchema(ctx, "test-session", srv, 999999, nil)
	require.ErrorContains(t, err, "missing shared result")
	c.testBeforeServeRequirementRecheck = func(row *sharedResult) {
		c.egraphMu.Lock()
		row.sessionResourceHandle = "late-socket"
		_, err := c.recomputeRequiredSessionResourcesLocked(row)
		c.egraphMu.Unlock()
		require.NoError(t, err)
	}
	_, err = c.LoadResultByResultIDForSchema(ctx, "test-session", srv, recorded, nil)
	require.ErrorContains(t, err, "session resources")
	c.testBeforeServeRequirementRecheck = nil
	require.NoError(t, c.ReleaseSession(ctx, "test-session"))
	_, err = c.LoadResultByResultIDForSchema(ctx, "test-session", srv, recorded, nil)
	require.ErrorIs(t, err, ErrCacheSessionReleased)
}

func TestSchemaModuleSelectionSkipsInaccessibleInstalled(t *testing.T) {
	for _, inaccessible := range []string{"operational", "scoped"} {
		t.Run(inaccessible, func(t *testing.T) {
			ctx, c, srv := transferTestCache(t)
			lower := persistedListTestResult(t, ctx, c, srv, "lower", String("lower"))
			recorded := persistedListTestResult(t, ctx, c, srv, "recorded", String("recorded"))
			operational := persistedListTestResult(t, ctx, c, srv, "operational", String("operational"))
			scoped := persistedListTestResult(t, ctx, c, srv, "scoped", String("scoped"))
			for _, value := range []AnyResult{lower, recorded, scoped} {
				_, err := value.WithContentDigestAny(ctx, digest.FromString("implementation"), call.ExtraDigestLabelRemoteCache)
				require.NoError(t, err)
			}
			c.egraphMu.Lock()
			blocked := operational.cacheSharedResult()
			if inaccessible == "scoped" {
				blocked = scoped.cacheSharedResult()
			}
			blocked.sessionResourceHandle = "unbound-socket"
			_, err := c.recomputeRequiredSessionResourcesLocked(blocked)
			c.egraphMu.Unlock()
			require.NoError(t, err)
			candidates := []SchemaModuleCandidate{{ModuleResultID: uint64(operational.cacheSharedResult().id), ScopedResultID: uint64(scoped.cacheSharedResult().id)}}
			id := uint64(recorded.cacheSharedResult().id)
			got, err := c.LoadResultByResultIDForSchema(ctx, "test-session", srv, id, candidates)
			require.NoError(t, err)
			require.Same(t, recorded.cacheSharedResult(), got.cacheSharedResult())
			c.egraphMu.Lock()
			recorded.cacheSharedResult().expiresAtUnix = time.Now().Add(-time.Hour).Unix()
			c.egraphMu.Unlock()
			got, err = c.LoadResultByResultIDForSchema(ctx, "test-session", srv, id, candidates)
			require.NoError(t, err)
			require.Same(t, lower.cacheSharedResult(), got.cacheSharedResult())
		})
	}
}

func testValueTransferCaptureConcurrent(t *testing.T) {
	t.Run("concurrent replacement and pruning", func(t *testing.T) {
		ctx, c, srv := transferTestCache(t)
		root := persistedListTestResult(t, ctx, c, srv, "root", &transferTestValue{Text: "pending"})
		old := persistedListTestResult(t, ctx, c, srv, "old", String("old"))
		next := persistedListTestResult(t, ctx, c, srv, "next", String("next"))
		transferTestOffer(t, c, ctx, root, old)
		entered, finish := make(chan struct{}), make(chan struct{})
		done := make(chan error, 1)
		go func() {
			done <- c.WithExportedValues(ctx, ValueSelection{Roots: []AnyResult{root}}, config.RefConfig{}, func(context.Context, *ExportedValues) error { close(entered); <-finish; return nil })
		}()
		<-entered
		c.egraphMu.Lock()
		owner, err := c.newOfferOwnerLocked(ctx, PersistedOfferOwner{DependencyIDs: []uint64{uint64(next.cacheSharedResult().id)}})
		var releases []OnReleaseFunc
		if err == nil {
			address := PersistedPartAddress{Part: "snapshot"}
			var queue []*sharedResult
			queue, err = c.replacePartOfferLocked(ctx, root.cacheSharedResult(), address, &partOffer{owner: owner, record: PersistedPartOffer{Address: address, Owner: owner.record, Value: SnapshotValue{Kind: "directory"}}})
			if err == nil {
				releases, err = c.collectUnownedResultsLocked(ctx, queue)
			}
		}
		c.egraphMu.Unlock()
		require.NoError(t, err)
		require.NoError(t, runOnReleaseFuncs(ctx, releases))
		for _, row := range []AnyResult{root, old, next} {
			_, err := c.removePersistedEdge(ctx, row.cacheSharedResult().id)
			require.NoError(t, err)
		}
		require.NoError(t, c.ReleaseSession(ctx, "test-session"))
		snapshot := c.snapshotPruneState(nil, pruneSnapshotMetadata, 10)
		require.Contains(t, pruneActiveClosure(snapshot, nil), old.cacheSharedResult().id)
		c.egraphMu.RLock()
		ownerCount := len(c.offerOwners)
		c.egraphMu.RUnlock()
		require.Equal(t, 2, ownerCount)
		close(finish)
		require.NoError(t, <-done)
		require.Empty(t, c.resultsByID)
		require.Empty(t, c.offerOwners)
	})
}

func testValueTransferImportConcurrent(t *testing.T) {
	t.Run("lookup at commit boundary", func(t *testing.T) {
		ctx, a, srv := transferTestCache(t)
		leaf := persistedListTestResult(t, ctx, a, srv, "leaf", String("value"))
		root := persistedListTestResult(t, ctx, a, srv, "root", DynamicResultArrayOutput{Elem: String(""), Values: []AnyResult{leaf}})
		bundle := exportTestBundle(t, ctx, a, root)
		ctx, b, bsrv := transferTestCache(t)
		entered, commit := make(chan struct{}), make(chan struct{})
		b.testBeforeTransferCommit = func() { close(entered); <-commit }
		done := make(chan error, 1)
		go func() { _, err := b.ImportValues(ctx, bundle); done <- err }()
		<-entered
		for _, value := range bundle.Values {
			_, err := b.LoadResultByResultID(ctx, "consumer", bsrv, uint64(value.Ordinal))
			require.ErrorContains(t, err, "missing shared result")
		}
		close(commit)
		require.NoError(t, <-done)
		b.egraphMu.RLock()
		resultCount := len(b.resultsByID)
		b.egraphMu.RUnlock()
		require.Equal(t, len(bundle.Values), resultCount)
		for _, value := range bundle.Values {
			_, err := b.LoadResultByResultID(ctx, "consumer", bsrv, uint64(value.Ordinal))
			require.NoError(t, err)
		}
	})
	t.Run("close waits for admitted import", func(t *testing.T) {
		ctx, a, srv := transferTestCache(t)
		bundle := exportTestBundle(t, ctx, a, persistedListTestResult(t, ctx, a, srv, "root", String("value")))
		ctx, b, _ := transferTestCache(t)
		entered, commit := make(chan struct{}), make(chan struct{})
		b.testBeforeTransferCommit = func() { close(entered); <-commit }
		done := make(chan error, 1)
		go func() { _, err := b.ImportValues(ctx, bundle); done <- err }()
		<-entered
		closed := make(chan error, 1)
		go func() { closed <- b.Close(ctx) }()
		select {
		case err := <-closed:
			t.Fatalf("close passed admitted import: %v", err)
		case <-time.After(10 * time.Millisecond):
		}
		close(commit)
		require.NoError(t, <-done)
		require.NoError(t, <-closed)
	})
}
