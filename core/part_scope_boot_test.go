package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/core/leases"
	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/stretchr/testify/require"
)

func TestPartScopeEmptyBoot(t *testing.T) {
	for _, oldSchema := range []bool{false, true} {
		t.Run(fmt.Sprintf("old_schema_%t", oldSchema), func(t *testing.T) {
			store := testutil.NewStore(t)
			dbPath := filepath.Join(t.TempDir(), "cache.db")
			ctx := t.Context()
			cache, err := dagql.NewCache(ctx, dbPath, store.Manager, nil)
			require.NoError(t, err)
			require.NoError(t, cache.Close(ctx))
			if oldSchema {
				db, err := sql.Open("sqlite", dbPath)
				require.NoError(t, err)
				_, err = db.Exec(`DROP TABLE result_snapshot_links`)
				require.NoError(t, err)
				_, err = db.Exec(`CREATE TABLE result_snapshot_links(result_id INTEGER NOT NULL,ref_key TEXT NOT NULL,role TEXT NOT NULL,PRIMARY KEY(result_id,ref_key,role)) STRICT, WITHOUT ROWID`)
				require.NoError(t, err)
				require.NoError(t, db.Close())
			}
			ref, _ := store.Build(t, nil, "data", "retained")
			stale := []string{"dagql/result/99/snapshot", "dagql/result/100/%5B%5B%5D%2C%22snapshot%22%5D"}
			for _, id := range append(stale, "unrelated/keep") {
				require.NoError(t, store.Manager.AttachLease(ctx, id, ref.SnapshotID()))
			}
			cache, err = dagql.NewCache(ctx, dbPath, store.Manager, nil)
			require.NoError(t, err)
			defer cache.CloseDiscardingPersistence()
			want := dagql.CachePersistenceResetNone
			if oldSchema {
				want = dagql.CachePersistenceResetImportFailure
			}
			require.Equal(t, want, cache.PersistenceResetReason())
			all, err := store.Leases.List(ctx)
			require.NoError(t, err)
			foundKeep := false
			for _, lease := range all {
				require.NotContains(t, stale, lease.ID)
				foundKeep = foundKeep || lease.ID == "unrelated/keep"
			}
			require.True(t, foundKeep)
		})
	}
}

type rekeyObservedManager struct {
	bkcache.SnapshotManager
	leases                         leases.Manager
	attaches, scans, removed, peak int
}

func (m *rekeyObservedManager) AttachLease(ctx context.Context, id, key string) error {
	m.attaches++
	return m.SnapshotManager.AttachLease(ctx, id, key)
}
func (m *rekeyObservedManager) DeleteStaleDaggerOwnerLeases(ctx context.Context, keep map[string]struct{}) error {
	m.scans++
	before, err := m.leases.List(ctx)
	if err != nil {
		return err
	}
	count := 0
	for _, l := range before {
		if strings.HasPrefix(l.ID, "dagql/result/") {
			count++
			if _, ok := keep[l.ID]; !ok {
				m.removed++
			}
		}
	}
	m.peak = max(m.peak, count)
	return m.SnapshotManager.DeleteStaleDaggerOwnerLeases(ctx, keep)
}
func TestPartScopeRootRekeyCost(t *testing.T) {
	for _, n := range []int{1, 32} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			store := testutil.NewStore(t)
			dbPath := filepath.Join(t.TempDir(), "cache.db")
			ctx, cache, srv := transferCache(t, store, dbPath, "initial")
			ref, _ := store.Build(t, nil, "data", "root lease")
			type root struct {
				id       uint64
				snapshot string
			}
			roots := make([]root, 0, n)
			for i := range n {
				copy, err := store.Manager.GetBySnapshotID(ctx, ref.SnapshotID())
				require.NoError(t, err)
				res := attachTransferObject(t, ctx, cache, srv, "initial", fmt.Sprintf("rekey%d", i), partTestDirectory(copy, "/"))
				id, err := cache.PersistedResultID(res)
				require.NoError(t, err)
				roots = append(roots, root{id, ref.SnapshotID()})
			}
			require.NoError(t, cache.ReleaseSession(ctx, "initial"))
			require.NoError(t, cache.Close(ctx))
			current, err := store.Leases.List(ctx)
			require.NoError(t, err)
			for _, r := range roots {
				require.NoError(t, store.Manager.AttachLease(ctx, fmt.Sprintf("dagql/result/%d/snapshot", r.id), r.snapshot))
			}
			for _, l := range current {
				if strings.HasPrefix(l.ID, "dagql/result/") {
					require.NoError(t, store.Manager.RemoveLease(ctx, l.ID))
				}
			}
			observed := &rekeyObservedManager{SnapshotManager: store.Manager, leases: store.Leases}
			start := time.Now()
			cache, err = dagql.NewCache(ctx, dbPath, observed, nil)
			elapsed := time.Since(start)
			require.NoError(t, err)
			defer cache.CloseDiscardingPersistence()
			require.Equal(t, n, observed.attaches)
			require.Equal(t, n, observed.removed)
			require.Equal(t, 2*n, observed.peak)
			require.Equal(t, 1, observed.scans)
			t.Logf("root_rekey roots=%d attaches=%d stale_scans=%d stale_removes=%d peak_owner_leases=%d elapsed=%s", n, observed.attaches, observed.scans, observed.removed, observed.peak, elapsed)
		})
	}
}

type failedScopeScan struct{ bkcache.SnapshotManager }

var errScopeScan = errors.New("stale owner lease scan failed")

func (failedScopeScan) DeleteStaleDaggerOwnerLeases(context.Context, map[string]struct{}) error {
	return errScopeScan
}
func TestPartScopeBootScanFailure(t *testing.T) {
	for _, persistent := range []bool{false, true} {
		store := testutil.NewStore(t)
		path := ""
		if persistent {
			path = filepath.Join(t.TempDir(), "cache.db")
		}
		cache, err := dagql.NewCache(t.Context(), path, failedScopeScan{store.Manager}, nil)
		require.ErrorIs(t, err, errScopeScan)
		require.Nil(t, cache, "a failed desired-set reconciliation must not admit lookups")
	}
}
