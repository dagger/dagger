package dagql

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// lateCopied runs the prefixes export with skip deciding, for each held row
// copied once the chains are open, whether to leave it unread, and returns
// the export and the rows copied that way, in order.
func lateCopied(t *testing.T, f capturePrefixes, skip func(*sharedResult) bool) (captureReadsExport, []uint64) {
	t.Helper()
	var mu sync.Mutex
	var late []uint64
	f.c.testCopyLate = func(res *sharedResult) bool {
		mu.Lock()
		late = append(late, uint64(res.id))
		mu.Unlock()
		return skip != nil && skip(res)
	}
	defer func() { f.c.testCopyLate = nil }()
	return f.run(t), late
}

// The held rows a content export copies once its chains are open are what
// keep its bundle what it was when every row was copied: leaving any one of
// them unread changes the bundle. A held row that changed since its walk
// when its record is copied there costs the export a pass, in which the copy
// reads it with the rows it carries in full: a change it finds there leaves
// out the roots that reach it, as when every row was copied, and the bundle
// is the same otherwise. No copied record names a snapshot its row's owner
// links don't.
func TestContentExportLateCopies(t *testing.T) {
	t.Parallel()

	t.Run("each one keeps the bundle", func(t *testing.T) {
		t.Parallel()
		got, late := lateCopied(t, capturePrefixesFixture(t), nil)
		assertCaptureReadsGolden(t, got, "prefixes")
		require.NotEmpty(t, late)
		require.Zero(t, got.unpredicted)
		require.Equal(t, len(got.copied), got.copiedRows, "each record copied once")
		want, err := json.MarshalIndent(got, "", "  ")
		require.NoError(t, err)
		for _, id := range late {
			skipped, _ := lateCopied(t, capturePrefixesFixture(t), func(res *sharedResult) bool { return uint64(res.id) == id })
			encoded, err := json.MarshalIndent(skipped, "", "  ")
			require.NoError(t, err)
			require.NotEqual(t, string(want), string(encoded), "leaving row %d unread changes the bundle", id)
		}
	})

	t.Run("a row that changed before its read", func(t *testing.T) {
		t.Parallel()
		f := capturePrefixesFixture(t)
		target := f.rows[3].cacheSharedResult()
		var once sync.Once
		got, late := lateCopied(t, f, func(res *sharedResult) bool {
			if res == target {
				once.Do(func() {
					res.payloadMu.Lock()
					res.payloadRevision++
					res.payloadMu.Unlock()
				})
			}
			return false
		})
		require.Equal(t, 2, got.Passes, "the export runs once more")
		require.Equal(t, 1, got.lateMoved)
		require.Equal(t, 1, countOf(late, uint64(target.id)), "read late once; the second pass reads it in its copy")
		got.Passes = 1
		assertCaptureReadsGolden(t, got, "prefixes")
	})

	t.Run("a row that changed and stays busy", func(t *testing.T) {
		t.Parallel()
		f := capturePrefixesFixture(t)
		target := f.rows[3].cacheSharedResult()
		got, _ := lateCopied(t, f, func(res *sharedResult) bool {
			if res == target {
				res.lazyMu.Lock()
				res.lazyWhole.syncPending = true
				res.lazyMu.Unlock()
				res.payloadMu.Lock()
				res.payloadRevision++
				res.payloadMu.Unlock()
			}
			return false
		})
		t.Cleanup(func() {
			target.lazyMu.Lock()
			target.lazyWhole.syncPending = false
			target.lazyMu.Unlock()
		})
		require.Equal(t, 2, got.Passes)
		require.Len(t, got.LeftOut, 1)
		require.Equal(t, 0, got.LeftOut[0].Index, "the top, which reaches the busy row, is left out")
		require.Equal(t, uint64(target.id), got.LeftOut[0].Row)
		require.Equal(t, BusyBookkeeping, got.LeftOut[0].Kind)

		// The same closure with the row busy from the start: its copy finds
		// it busy, as when every row was copied.
		g := capturePrefixesFixture(t)
		busy := g.rows[3].cacheSharedResult()
		busy.lazyMu.Lock()
		busy.lazyWhole.syncPending = true
		busy.lazyMu.Unlock()
		t.Cleanup(func() {
			busy.lazyMu.Lock()
			busy.lazyWhole.syncPending = false
			busy.lazyMu.Unlock()
		})
		want := g.run(t)
		require.Equal(t, 1, want.Passes)
		got.Passes = want.Passes
		wantJSON, err := json.MarshalIndent(want, "", "  ")
		require.NoError(t, err)
		gotJSON, err := json.MarshalIndent(got, "", "  ")
		require.NoError(t, err)
		require.Equal(t, string(wantJSON), string(gotJSON), "the bundle and left-out roots of a row busy from the start")
	})
}

func countOf(ids []uint64, id uint64) int {
	n := 0
	for _, x := range ids {
		if x == id {
			n++
		}
	}
	return n
}
