package remotecache

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/remotecache/protocol"
)

// The rows an export carried in full become known to the connection when the
// service says it merged the export, and only then; the session hands its
// known set to every export's capture. An Upload without Merged makes the
// export's held rows unknown again. A new connection knows nothing.
func TestExportKnownRows(t *testing.T) {
	rows := map[uint64]dagql.ExportedRows{
		1: {Full: map[uint64]dagql.RowRevision{12: {Replacements: 1, Transfer: 2, Dependencies: 3}, 13: {}}},
		2: {Full: map[uint64]dagql.RowRevision{14: {}}, Held: []uint64{12}},
		3: {Full: map[uint64]dagql.RowRevision{15: {}}},
	}
	adapter := newFakeAdapter()
	adapter.export = func(ctx context.Context, req protocol.Export, consume func(context.Context, Export) error) error {
		return consume(ctx, Export{Bundle: testBundle(), Rows: rows[req.Roots[0]]})
	}
	var id uint64
	// export asks s for root's records, answers with upload unless it is
	// nil, and returns the known set the capture got.
	export := func(s *session, conn *recordingConn, root uint64, upload *protocol.Upload) *dagql.KnownRows {
		t.Helper()
		id++
		s.dispatch(request(t, id, protocol.TypeExport, protocol.Export{Roots: []uint64{root}}))
		exported := decode[protocol.Exported](t, conn.next(t))
		require.NotEmpty(t, exported.ExportID)
		known := adapter.known.Load()
		require.NotNil(t, known)
		if upload != nil {
			id++
			upload.ExportID = exported.ExportID
			s.dispatch(request(t, id, protocol.TypeUpload, *upload))
			require.Equal(t, protocol.TypeUploaded, conn.next(t).Type)
		}
		return known
	}
	s, conn := testSession(t, adapter)
	known := export(s, conn, 1, &protocol.Upload{})
	require.Zero(t, known.Len(), "an export the service didn't merge marks nothing")
	require.Same(t, known, export(s, conn, 1, &protocol.Upload{Merged: true}))
	require.Equal(t, 2, known.Len(), "the rows a merged export carried in full are known")
	require.Same(t, known, export(s, conn, 2, &protocol.Upload{Merged: true}))
	require.Equal(t, 3, known.Len(), "a merged export's held rows stay known")
	export(s, conn, 2, &protocol.Upload{})
	require.Equal(t, 2, known.Len(), "an export the service didn't merge makes its held rows unknown")
	export(s, conn, 3, nil)
	require.Equal(t, 2, known.Len(), "an export with no Upload marks nothing")

	other, otherConn := testSession(t, adapter)
	fresh := export(other, otherConn, 1, nil)
	require.NotSame(t, known, fresh)
	require.Zero(t, fresh.Len(), "a new connection knows nothing")
}

// The session hands every merge from the service the connection's known set,
// the one its exports get, so that the rows a merge lands on become known:
// the service holds the records it sent.
func TestMergeKnownRows(t *testing.T) {
	adapter := newFakeAdapter()
	adapter.merge = func(context.Context, protocol.Merge) (protocol.Merged, error) {
		return protocol.Merged{}, nil
	}
	adapter.export = func(ctx context.Context, req protocol.Export, consume func(context.Context, Export) error) error {
		return consume(ctx, Export{Bundle: testBundle()})
	}
	s, conn := testSession(t, adapter)
	s.dispatch(request(t, 1, protocol.TypeMerge, protocol.Merge{Bundle: *testBundle()}))
	require.Equal(t, protocol.TypeMerged, conn.next(t).Type)
	merged := adapter.mergeKnown.Load()
	require.NotNil(t, merged, "a merge gets the connection's known set")

	s.dispatch(request(t, 2, protocol.TypeExport, protocol.Export{Roots: []uint64{1}}))
	require.Equal(t, protocol.TypeExported, conn.next(t).Type)
	require.Same(t, adapter.known.Load(), merged, "the same set an export gets")
}

// An Upload without Merged that names the held rows the service found stale
// makes only those unknown: the export's other held rows stay known, so the
// next export carries the stale ones in full and holds the rest. On a whole
// pull request's fill, test-base's engine forgot thousands of held rows for
// each 2 to 108 the service named, until a test run's export, carrying its
// closure in full, no longer fit in a message.
func TestExportStaleForgetsOnlyTheStaleRows(t *testing.T) {
	rows := map[uint64]dagql.ExportedRows{
		1: {Full: map[uint64]dagql.RowRevision{12: {}, 13: {}, 14: {}}},
		2: {Full: map[uint64]dagql.RowRevision{15: {}}, Held: []uint64{12, 13, 14}},
	}
	adapter := newFakeAdapter()
	adapter.export = func(ctx context.Context, req protocol.Export, consume func(context.Context, Export) error) error {
		return consume(ctx, Export{Bundle: testBundle(), Rows: rows[req.Roots[0]]})
	}
	var id uint64
	export := func(s *session, conn *recordingConn, root uint64, upload protocol.Upload) *dagql.KnownRows {
		t.Helper()
		id++
		s.dispatch(request(t, id, protocol.TypeExport, protocol.Export{Roots: []uint64{root}}))
		exported := decode[protocol.Exported](t, conn.next(t))
		known := adapter.known.Load()
		id++
		upload.ExportID = exported.ExportID
		s.dispatch(request(t, id, protocol.TypeUpload, upload))
		require.Equal(t, protocol.TypeUploaded, conn.next(t).Type)
		return known
	}
	s, conn := testSession(t, adapter)
	known := export(s, conn, 1, protocol.Upload{Merged: true})
	require.Equal(t, 3, known.Len())
	export(s, conn, 2, protocol.Upload{Stale: []uint64{13}})
	require.Equal(t, 2, known.Len(), "only the row the service named stale is unknown")
	export(s, conn, 2, protocol.Upload{})
	require.Zero(t, known.Len(), "an Upload without Merged that names none makes every held row unknown")
}
