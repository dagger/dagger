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
