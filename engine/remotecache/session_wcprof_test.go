package remotecache

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/engine/remotecache/protocol"
	"github.com/dagger/dagger/engine/wcprof"
)

// profiledEvent is a dumped event with its strings resolved.
type profiledEvent struct {
	wcprof.DumpEvent
	class, ident string
}

// profiledSession is a session whose work is recorded, as a session on an
// engine recording everything is.
func profiledSession(t *testing.T, adapter Adapter) (*session, *recordingConn) {
	t.Helper()
	wcprof.EnsureRecorder()
	conn := newRecordingConn()
	cfg := newSettings(Config{EngineVersion: "v0.20.0", EngineName: "dagger-engine"})
	s := newSession(wcprof.ContextWithProfiling(t.Context()), conn, adapter, cfg)
	t.Cleanup(s.close)
	return s, conn
}

// profiledExport returns the dumped events of the export op named exportID
// and everything under it, once the op has ended, keyed by op ID.
func profiledExport(t *testing.T, exportID string) (profiledEvent, []profiledEvent) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var buf bytes.Buffer
		require.NoError(t, wcprof.Active().WriteDump(&buf, false))
		header, events, err := wcprof.ReadDump(&buf)
		require.NoError(t, err)
		var root profiledEvent
		parents := map[uint64]uint64{}
		for _, ev := range events {
			if ev.Type != "op" {
				continue
			}
			parents[ev.OpID] = ev.ParentID
			if header.Strings[ev.ClassID] == "remotecache.export" && header.Strings[ev.IdentID] == exportID {
				root = profiledEvent{DumpEvent: ev, class: "remotecache.export", ident: exportID}
			}
		}
		if root.OpID != 0 {
			var out []profiledEvent
			for _, ev := range events {
				id := ev.ParentID
				if ev.Type == "op" {
					id = ev.OpID
				}
				for ; id != 0 && id != root.OpID; id = parents[id] {
				}
				if id == 0 || ev.OpID == root.OpID {
					continue
				}
				out = append(out, profiledEvent{DumpEvent: ev, class: header.Strings[ev.ClassID], ident: header.Strings[ev.IdentID]})
			}
			return root, out
		}
		if time.Now().After(deadline) {
			t.Fatalf("no export op %q", exportID)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A profiled export is one root op named by its export ID, from the request
// to the chains' release, with the bytes uploaded. It waited for an export
// slot and for the service's upload request; its upload is a child op, and
// each blob's PUT a child of that with the blob's size, after a wait for an
// upload slot. The adapter's capture runs under it.
func TestExportProfiled(t *testing.T) {
	d1, d2 := digest.FromString("one"), digest.FromString("two")
	data := blobs{d1: "first", d2: "second"}
	adapter := newFakeAdapter()
	var adapterOp uint64
	adapter.export = func(ctx context.Context, _ protocol.Export, consume func(context.Context, Export) error) error {
		adapterOp = wcprof.CurrentOpID(ctx)
		return consume(ctx, Export{Bundle: testBundle(), Blobs: data})
	}
	s, conn := profiledSession(t, adapter)
	s.cfg.put = func(_ context.Context, _ string, body io.Reader, _ int64) error {
		_, err := io.Copy(io.Discard, body)
		return err
	}
	s.dispatch(request(t, 7, protocol.TypeExport, protocol.Export{Roots: []uint64{12}}))
	exportID := decode[protocol.Exported](t, conn.next(t)).ExportID
	require.NotEmpty(t, exportID)
	s.dispatch(request(t, 8, protocol.TypeUpload, protocol.Upload{ExportID: exportID, URLs: map[digest.Digest]string{d1: "put-1", d2: "put-2"}}))
	require.Equal(t, protocol.TypeUploaded, conn.next(t).Type)

	export, events := profiledExport(t, exportID)
	require.Equal(t, "remote_cache", export.OpKind)
	require.Equal(t, "ok", export.Outcome)
	require.Zero(t, export.ParentID, "an export is the service's, not a client's")
	require.EqualValues(t, len("first")+len("second"), export.Bytes)
	require.Equal(t, export.OpID, adapterOp, "the adapter runs under the export")

	waits := map[string]int{}
	var upload profiledEvent
	blobOps := map[string]profiledEvent{}
	for _, ev := range events {
		switch {
		case ev.Type == "wait":
			waits[ev.Reason+" "+ev.ident]++
			require.GreaterOrEqual(t, ev.StartNS, export.StartNS, "%s %s starts inside the export", ev.Reason, ev.ident)
			require.LessOrEqual(t, ev.EndNS, export.EndNS, "%s %s ends inside the export", ev.Reason, ev.ident)
		case ev.Type == "op" && ev.class == "remotecache.upload":
			upload = ev
		case ev.Type == "op" && ev.class == "remotecache.upload.blob":
			blobOps[ev.ident] = ev
		}
	}
	require.Equal(t, map[string]int{
		"lock remotecache.exportSlots":  1,
		"io remotecache:service-upload": 1,
		"lock remotecache.uploadSlots":  2,
	}, waits)
	require.Equal(t, export.OpID, upload.ParentID)
	require.Equal(t, export.Bytes, upload.Bytes)
	require.Equal(t, "external", upload.WorkType)
	require.Len(t, blobOps, 2)
	for dgst, body := range data {
		op := blobOps[dgst.String()]
		require.Equal(t, upload.OpID, op.ParentID)
		require.EqualValues(t, len(body), op.Bytes)
		require.Equal(t, "ok", op.Outcome)
		require.Equal(t, "external", op.WorkType)
	}
}

// An export with no bundle, or whose upload asks for no blobs, says so as a
// fact on its op.
func TestExportProfiledFacts(t *testing.T) {
	t.Run("no bundle", func(t *testing.T) {
		adapter := newFakeAdapter()
		adapter.export = func(ctx context.Context, _ protocol.Export, consume func(context.Context, Export) error) error {
			return consume(ctx, Export{Skipped: []protocol.SkippedRoot{{Number: 12, Reason: protocol.SkipGone}}})
		}
		s, conn := profiledSession(t, adapter)
		s.dispatch(request(t, 7, protocol.TypeExport, protocol.Export{Roots: []uint64{12}}))
		require.Empty(t, decode[protocol.Exported](t, conn.next(t)).ExportID)
		export, events := profiledExport(t, "")
		require.Equal(t, "ok", export.Outcome)
		requireFact(t, export, events, "export.no_bundle")
	})
	t.Run("empty", func(t *testing.T) {
		adapter := newFakeAdapter()
		adapter.export = func(ctx context.Context, _ protocol.Export, consume func(context.Context, Export) error) error {
			return consume(ctx, Export{Bundle: testBundle(), Blobs: blobs{}})
		}
		s, conn := profiledSession(t, adapter)
		s.dispatch(request(t, 7, protocol.TypeExport, protocol.Export{Roots: []uint64{12}}))
		exportID := decode[protocol.Exported](t, conn.next(t)).ExportID
		s.dispatch(request(t, 8, protocol.TypeUpload, protocol.Upload{ExportID: exportID, URLs: map[digest.Digest]string{}}))
		require.Equal(t, protocol.TypeUploaded, conn.next(t).Type)
		export, events := profiledExport(t, exportID)
		requireFact(t, export, events, "export.empty")
	})
}

func requireFact(t *testing.T, op profiledEvent, events []profiledEvent, fact string) {
	t.Helper()
	var facts []string
	for _, ev := range events {
		if ev.Type == "link" && ev.LinkKind == "fact" && ev.ParentID == op.OpID {
			facts = append(facts, ev.ident)
		}
	}
	require.Equal(t, []string{fact}, facts)
}
