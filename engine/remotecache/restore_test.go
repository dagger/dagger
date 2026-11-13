package remotecache

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/remotecache/protocol"
	"github.com/dagger/dagger/engine/snapshots"
)

// These tests drive a session's restores through dispatch, with set files
// served over HTTP and parts merged by the fake adapter.

// setFile is a set file of the parts, each encoded as the service encodes
// it, at the bundle version.
func setFile(t *testing.T, version int, parts ...dagql.ValueBundle) []byte {
	t.Helper()
	enc, err := zstd.NewWriter(nil)
	require.NoError(t, err)
	defer enc.Close()
	header := protocol.SetFileHeader{Version: version, Check: "test-base"}
	var raw [][]byte
	for _, part := range parts {
		data, entry, err := protocol.EncodeSetFilePart(enc, part)
		require.NoError(t, err)
		raw = append(raw, data)
		header.Parts = append(header.Parts, entry)
	}
	var buf bytes.Buffer
	require.NoError(t, protocol.WriteSetFile(&buf, header, raw))
	return buf.Bytes()
}

// part is a bundle whose one record is sender's.
func part(sender uint64) dagql.ValueBundle {
	return dagql.ValueBundle{Version: dagql.ValueBundleVersion, Roots: []dagql.TransferredRoot{{Ordinal: 1}}, Values: []dagql.TransferredValue{{Ordinal: 1, SenderNumber: sender}}}
}

// fileServer serves set files by name, counting each one's GETs. A file
// whose gate is set is sent only once the gate closes.
type fileServer struct {
	*httptest.Server
	mu    sync.Mutex
	files map[string][]byte
	gets  map[string]int
	gates map[string]chan struct{}
}

func newFileServer(t *testing.T) *fileServer {
	fs := &fileServer{files: map[string][]byte{}, gets: map[string]int{}, gates: map[string]chan struct{}{}}
	fs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		fs.mu.Lock()
		data, ok := fs.files[name]
		fs.gets[name]++
		gate := fs.gates[name]
		fs.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		if gate != nil {
			select {
			case <-gate:
			case <-r.Context().Done():
				return
			}
		}
		_, _ = w.Write(data)
	}))
	t.Cleanup(fs.Close)
	return fs
}

// add serves data as name, and returns the file a restore names for it.
func (fs *fileServer) add(name string, data []byte) protocol.SetFile {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.files[name] = data
	return protocol.SetFile{Digest: digest.FromBytes(data), Size: int64(len(data)), Address: dagql.BlobAddress{URL: fs.URL + "/" + name}}
}

func (fs *fileServer) gate(name string) chan struct{} {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	gate := make(chan struct{})
	fs.gates[name] = gate
	return gate
}

func (fs *fileServer) getsOf(name string) int {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.gets[name]
}

// restoreSession is a session whose restores run, as serve runs them.
func restoreSession(t *testing.T, adapter *fakeAdapter) (*session, *recordingConn) {
	t.Helper()
	s, conn := testSession(t, adapter)
	s.cfg.peakRSS = func() int64 { return 0 }
	s.go_(s.restorer.run)
	return s, conn
}

// merged is a commit's reply for a part: one value per record, on the
// sender's number, each made new.
func merged(part dagql.ValueBundle, at time.Time) dagql.MergeReply {
	reply := dagql.MergeReply{Committed: true, EngineTimeUnixNano: at.UnixNano(), Rows: dagql.MergeRowCounts{Created: len(part.Values)}, LockWait: time.Millisecond, CommitHold: 2 * time.Millisecond}
	for _, value := range part.Values {
		reply.Values = append(reply.Values, dagql.MergedValue{Ordinal: value.Ordinal, Number: value.SenderNumber})
	}
	return reply
}

func restoreOf(check string, head, whole *protocol.SetFile) protocol.Restore {
	return protocol.Restore{Sets: []protocol.RestoreSet{{Check: check, Head: head, Whole: whole}}}
}

// seen returns the sender numbers of the next n parts the adapter merged.
func seen(t *testing.T, adapter *fakeAdapter, n int) []uint64 {
	t.Helper()
	var senders []uint64
	for range n {
		select {
		case part := <-adapter.restoreSeen:
			senders = append(senders, part.Values[0].SenderNumber)
		case <-time.After(10 * time.Second):
			t.Fatalf("merged %d parts of %d", len(senders), n)
		}
	}
	return senders
}

func nextReport(t *testing.T, adapter *fakeAdapter) RestoreReport {
	t.Helper()
	select {
	case report := <-adapter.restored:
		return report
	case <-time.After(10 * time.Second):
		t.Fatal("no file's restore ended")
		return RestoreReport{}
	}
}

// A restore fetches its file, checks it, and merges its parts in order, one
// at a time, through the adapter, with the connection's known rows; it
// answers with what merged, and reports the file for the stop event.
func TestRestoreMergesAFilesParts(t *testing.T) {
	t.Parallel()
	adapter := newFakeAdapter()
	commit := time.Unix(1790000000, 0)
	adapter.restore = func(_ context.Context, part dagql.ValueBundle, _ *dagql.KnownRows) (dagql.MergeReply, error) {
		return merged(part, commit), nil
	}
	s, conn := restoreSession(t, adapter)
	files := newFileServer(t)
	whole := files.add("whole", setFile(t, dagql.ValueBundleVersion, part(11), part(12)))

	s.dispatch(request(t, 7, protocol.TypeRestore, restoreOf("test-base", nil, &whole)))
	require.Equal(t, []uint64{11, 12}, seen(t, adapter, 2))
	reply := conn.next(t)
	require.Equal(t, protocol.TypeRestored, reply.Type)
	require.Equal(t, uint64(7), reply.Re)
	restored := decode[protocol.Restored](t, reply)
	require.Equal(t, uint64(3), restored.Generation)
	require.Equal(t, []protocol.RestoredFile{{
		Digest:      whole.Digest,
		Complete:    true,
		Parts:       2,
		Records:     2,
		Rows:        protocol.RowCounts{Created: 2},
		FirstCommit: commit.UnixNano(),
	}}, restored.Files)

	report := nextReport(t, adapter)
	require.Equal(t, "whole", report.Kind)
	require.Equal(t, "test-base", report.Check)
	require.Equal(t, whole.Digest, report.Digest)
	require.True(t, report.Complete)
	require.Len(t, report.Parts, 2)
	require.Equal(t, commit, report.Parts[1].Committed)
	require.Equal(t, time.Millisecond, report.Parts[1].LockWait, "the part's wait for the cache's lock")
	require.Equal(t, 2*time.Millisecond, report.Parts[1].CommitHold, "and its hold of it")
	require.False(t, report.FetchStarted.IsZero())
	require.False(t, report.Fetched.Before(report.FetchStarted))
	require.Equal(t, 1, files.getsOf("whole"))
}

// The rows a part lands on join the connection's known rows, as a merge's
// do: the adapter is given the session's.
func TestRestoreUsesTheConnectionsKnownRows(t *testing.T) {
	t.Parallel()
	adapter := newFakeAdapter()
	knownSeen := make(chan *dagql.KnownRows, 1)
	adapter.restore = func(_ context.Context, part dagql.ValueBundle, known *dagql.KnownRows) (dagql.MergeReply, error) {
		knownSeen <- known
		return merged(part, time.Now()), nil
	}
	s, conn := restoreSession(t, adapter)
	files := newFileServer(t)
	head := files.add("head", setFile(t, dagql.ValueBundleVersion, part(1)))
	s.dispatch(request(t, 1, protocol.TypeRestore, restoreOf("c", &head, nil)))
	require.Same(t, s.known, <-knownSeen)
	require.Equal(t, protocol.TypeRestored, conn.next(t).Type)
}

// A part's layers get their download addresses from the file's table: a
// layer its chain stores in a pack the pack's, on that chain, and any other
// its blob's, in the bundle's; one the table lacks is left for renewal.
func TestRestoreFillsAddressesFromTheTable(t *testing.T) {
	t.Parallel()
	alone := snapshots.ExportLayer{Descriptor: ocispecs.Descriptor{MediaType: ocispecs.MediaTypeImageLayerZstd, Digest: digest.FromString("alone"), Size: 10}}
	packed := snapshots.ExportLayer{Descriptor: ocispecs.Descriptor{MediaType: ocispecs.MediaTypeImageLayerZstd, Digest: digest.FromString("packed"), Size: 20}}
	unsigned := snapshots.ExportLayer{Descriptor: ocispecs.Descriptor{MediaType: ocispecs.MediaTypeImageLayerZstd, Digest: digest.FromString("unsigned"), Size: 30}}
	pack := digest.FromString("pack")
	bundle := part(5)
	bundle.Outputs = []dagql.TransferredOutput{{
		Ordinal: 1,
		Address: dagql.PersistedPartAddress{OutputPath: dagql.PersistedRefPath{}.Field("rootfs"), Part: "fs"},
		State:   "complete",
		Chain: &dagql.OfferedChain{
			Layers:     []snapshots.ExportLayer{alone, packed, unsigned},
			Packs:      map[digest.Digest]digest.Digest{packed.Descriptor.Digest: pack},
			RenewalKey: "5/fs",
		},
	}}
	adapter := newFakeAdapter()
	adapter.restore = func(_ context.Context, part dagql.ValueBundle, _ *dagql.KnownRows) (dagql.MergeReply, error) {
		return merged(part, time.Now()), nil
	}
	s, conn := restoreSession(t, adapter)
	files := newFileServer(t)
	file := files.add("whole", setFile(t, dagql.ValueBundleVersion, bundle))
	file.Table = protocol.AddressTable{
		Blobs: map[digest.Digest]dagql.BlobAddress{alone.Descriptor.Digest: {URL: "https://blobs/alone", ExpiresAtUnix: 1790003600}},
		Packs: map[digest.Digest]dagql.BlobAddress{pack: {URL: "https://blobs/pack", ExpiresAtUnix: 1790003600}},
	}
	s.dispatch(request(t, 1, protocol.TypeRestore, restoreOf("c", nil, &file)))
	got := <-adapter.restoreSeen
	require.Equal(t, protocol.TypeRestored, conn.next(t).Type)

	require.Equal(t, map[digest.Digest]dagql.BlobAddress{alone.Descriptor.Digest: {URL: "https://blobs/alone", ExpiresAtUnix: 1790003600}}, got.Addresses)
	chain := got.Outputs[0].Chain
	require.Equal(t, map[digest.Digest]dagql.BlobAddress{packed.Descriptor.Digest: {URL: "https://blobs/pack", ExpiresAtUnix: 1790003600}}, chain.Addresses)
	require.Equal(t, "5/fs", chain.RenewalKey)
	require.Len(t, got.Outputs[0].Layers, 3, "the part names its layers by place, as a merge's body does")
}

// Heads and wholes merge on lanes of their own: two claims' heads, offered
// together with their wholes, both commit while the first whole is still
// merging, and so does the head of a claim that arrives meanwhile. Each
// lane keeps its arrival order.
func TestRestoreMergesHeadsBesideWholes(t *testing.T) {
	t.Parallel()
	adapter := newFakeAdapter()
	release := make(chan struct{})
	adapter.restore = func(_ context.Context, part dagql.ValueBundle, _ *dagql.KnownRows) (dagql.MergeReply, error) {
		if part.Values[0].SenderNumber == 101 {
			<-release
		}
		return merged(part, time.Now()), nil
	}
	s, conn := restoreSession(t, adapter)
	files := newFileServer(t)
	headA := files.add("headA", setFile(t, dagql.ValueBundleVersion, part(1)))
	wholeA := files.add("wholeA", setFile(t, dagql.ValueBundleVersion, part(101), part(102)))
	headB := files.add("headB", setFile(t, dagql.ValueBundleVersion, part(2)))
	wholeB := files.add("wholeB", setFile(t, dagql.ValueBundleVersion, part(201)))
	headC := files.add("headC", setFile(t, dagql.ValueBundleVersion, part(3), part(4)))

	// Two claims offered together, each head then whole.
	s.dispatch(request(t, 1, protocol.TypeRestore, restoreOf("a", &headA, nil)))
	s.dispatch(request(t, 2, protocol.TypeRestore, restoreOf("a", nil, &wholeA)))
	s.dispatch(request(t, 3, protocol.TypeRestore, restoreOf("b", &headB, nil)))
	s.dispatch(request(t, 4, protocol.TypeRestore, restoreOf("b", nil, &wholeB)))
	got := seen(t, adapter, 3)
	require.ElementsMatch(t, []uint64{1, 2, 101}, got, "both heads merge while wholeA's first part is merging")
	require.Less(t, slices.Index(got, 1), slices.Index(got, 2), "heads keep their order")
	answered := []uint64{conn.next(t).Re, conn.next(t).Re}
	require.Equal(t, []uint64{1, 3}, answered)

	// A claim that arrives while wholeA is still merging.
	s.dispatch(request(t, 5, protocol.TypeRestore, restoreOf("c", &headC, nil)))
	require.Equal(t, []uint64{3, 4}, seen(t, adapter, 2))
	require.Equal(t, uint64(5), conn.next(t).Re)
	conn.none(t)

	close(release)
	require.Equal(t, []uint64{102, 201}, seen(t, adapter, 2), "wholes keep their order")
	require.Equal(t, []uint64{2, 4}, []uint64{conn.next(t).Re, conn.next(t).Re})
}

// A whole whose download hasn't finished doesn't hold up a head that
// arrives meanwhile.
func TestRestoreHeadDoesntWaitForAWholesDownload(t *testing.T) {
	t.Parallel()
	adapter := newFakeAdapter()
	adapter.restore = func(_ context.Context, part dagql.ValueBundle, _ *dagql.KnownRows) (dagql.MergeReply, error) {
		return merged(part, time.Now()), nil
	}
	s, conn := restoreSession(t, adapter)
	files := newFileServer(t)
	whole := files.add("whole", setFile(t, dagql.ValueBundleVersion, part(1)))
	gate := files.gate("whole")
	head := files.add("head", setFile(t, dagql.ValueBundleVersion, part(2)))

	s.dispatch(request(t, 1, protocol.TypeRestore, restoreOf("a", nil, &whole)))
	s.dispatch(request(t, 2, protocol.TypeRestore, restoreOf("b", &head, nil)))
	require.Equal(t, []uint64{2}, seen(t, adapter, 1))
	require.Equal(t, uint64(2), conn.next(t).Re)
	close(gate)
	require.Equal(t, []uint64{1}, seen(t, adapter, 1))
	require.Equal(t, uint64(1), conn.next(t).Re)
}

// Nothing in a restore fails it: a file that can't be fetched, isn't the
// file named, is of another bundle version, or has a part that doesn't
// decode or merge is answered with the error that stopped it and what
// merged before, and the next restore goes on.
func TestRestoreReportsWhatStoppedAFile(t *testing.T) {
	t.Parallel()
	good := setFile(t, dagql.ValueBundleVersion, part(1), part(2), part(3))
	for _, tc := range []struct {
		name  string
		file  func(*fileServer) protocol.SetFile
		parts int
		err   string
	}{
		{"missing", func(fs *fileServer) protocol.SetFile {
			file := fs.add("missing", good)
			fs.mu.Lock()
			delete(fs.files, "missing")
			fs.mu.Unlock()
			return file
		}, 0, "404"},
		{"other bytes", func(fs *fileServer) protocol.SetFile {
			file := fs.add("other", good)
			file.Digest = digest.FromString("another file")
			return file
		}, 0, "the bytes don't match the file's digest"},
		{"short", func(fs *fileServer) protocol.SetFile {
			file := fs.add("short", good)
			file.Size++
			return file
		}, 0, "bytes, the restore says"},
		{"another version", func(fs *fileServer) protocol.SetFile {
			return fs.add("v9", setFile(t, dagql.ValueBundleVersion+1, part(1)))
		}, 0, "unsupported value bundle version"},
		{"undecodable part", func(fs *fileServer) protocol.SetFile {
			data := setFile(t, dagql.ValueBundleVersion, part(1), part(2))
			data[len(data)-3] ^= 0xff
			return fs.add("corrupt", data)
		}, 1, "part 1"},
		{"refused part", func(fs *fileServer) protocol.SetFile {
			return fs.add("refused", good)
		}, 1, "part 1: refused"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			adapter := newFakeAdapter()
			adapter.restore = func(_ context.Context, part dagql.ValueBundle, _ *dagql.KnownRows) (dagql.MergeReply, error) {
				if part.Values[0].SenderNumber == 2 {
					return dagql.MergeReply{}, errors.New("refused")
				}
				return merged(part, time.Now()), nil
			}
			s, conn := restoreSession(t, adapter)
			files := newFileServer(t)
			bad := tc.file(files)
			next := files.add("next", setFile(t, dagql.ValueBundleVersion, part(9)))

			s.dispatch(request(t, 1, protocol.TypeRestore, restoreOf("c", nil, &bad)))
			s.dispatch(request(t, 2, protocol.TypeRestore, restoreOf("c", nil, &next)))
			reply := conn.next(t)
			require.Equal(t, protocol.TypeRestored, reply.Type, "a failed file is answered, not refused")
			file := decode[protocol.Restored](t, reply).Files[0]
			require.False(t, file.Complete)
			require.Equal(t, tc.parts, file.Parts)
			require.Contains(t, file.Error, tc.err)
			report := nextReport(t, adapter)
			require.Equal(t, file.Error, report.Error)
			require.Len(t, report.Parts, tc.parts)

			reply = conn.next(t)
			require.Equal(t, uint64(2), reply.Re)
			require.True(t, decode[protocol.Restored](t, reply).Files[0].Complete, "the next restore goes on")
		})
	}
}

// A download the store stops sending is given up after the idle bound, as
// a part's read is, and reported.
func TestRestoreGivesUpAStalledDownload(t *testing.T) {
	t.Parallel()
	adapter := newFakeAdapter()
	s, conn := restoreSession(t, adapter)
	s.cfg.getFile = getSetFile(&http.Client{}, 50*time.Millisecond)
	files := newFileServer(t)
	file := files.add("stalled", setFile(t, dagql.ValueBundleVersion, part(1)))
	files.gate("stalled")
	s.dispatch(request(t, 1, protocol.TypeRestore, restoreOf("c", nil, &file)))
	got := decode[protocol.Restored](t, conn.next(t)).Files[0]
	require.False(t, got.Complete)
	require.Contains(t, got.Error, "stopped sending")
}

// A file this engine process merged whole is answered at once, with no
// download, on this connection or a later one, and each hello names it.
func TestRestoreOfAFileMergedWholeDoesNothing(t *testing.T) {
	t.Parallel()
	adapter := newFakeAdapter()
	adapter.restore = func(_ context.Context, part dagql.ValueBundle, _ *dagql.KnownRows) (dagql.MergeReply, error) {
		return merged(part, time.Now()), nil
	}
	s, conn := restoreSession(t, adapter)
	files := newFileServer(t)
	file := files.add("whole", setFile(t, dagql.ValueBundleVersion, part(1)))
	s.dispatch(request(t, 1, protocol.TypeRestore, restoreOf("c", nil, &file)))
	require.True(t, decode[protocol.Restored](t, conn.next(t)).Files[0].Complete)
	nextReport(t, adapter)

	s.dispatch(request(t, 2, protocol.TypeRestore, restoreOf("c", nil, &file)))
	again := decode[protocol.Restored](t, conn.next(t)).Files[0]
	require.Equal(t, protocol.RestoredFile{Digest: file.Digest, Complete: true, Already: true}, again)
	require.True(t, nextReport(t, adapter).Already)
	require.Equal(t, 1, files.getsOf("whole"))

	// A later connection of the same process says so in its hello.
	next := newSession(t.Context(), newRecordingConn(), adapter, s.cfg)
	t.Cleanup(next.close)
	go func() { _, _ = next.serve() }()
	hello := next.conn.(*recordingConn).next(t)
	require.Equal(t, []digest.Digest{file.Digest}, decode[protocol.Hello](t, hello).Restored)
}

// A part counts the lookups that missed before it committed and that it
// would have served, as the adapter finds them on the entries it landed
// on, with the longest and shortest time from a miss to the commit.
func TestRestoreCountsLateMisses(t *testing.T) {
	t.Parallel()
	commit := time.Unix(1790000000, 0)
	adapter := newFakeAdapter()
	adapter.restore = func(_ context.Context, part dagql.ValueBundle, _ *dagql.KnownRows) (dagql.MergeReply, error) {
		return merged(part, commit), nil
	}
	type lateCall struct {
		before  time.Time
		entries map[uint64]struct{}
	}
	calls := make(chan lateCall, 4)
	adapter.late = func(before time.Time, entries map[uint64]struct{}) []dagql.RemoteMiss {
		calls <- lateCall{before, entries}
		if _, ok := entries[1]; !ok {
			return nil
		}
		return []dagql.RemoteMiss{{Recipe: "xxh3:1", At: commit.Add(-3 * time.Second)}, {Recipe: "xxh3:2", At: commit.Add(-time.Second)}}
	}
	s, conn := restoreSession(t, adapter)
	files := newFileServer(t)
	head := files.add("head", setFile(t, dagql.ValueBundleVersion, part(1), part(2)))
	s.dispatch(request(t, 1, protocol.TypeRestore, restoreOf("c", &head, nil)))
	require.Equal(t, protocol.TypeRestored, conn.next(t).Type)
	first := <-calls
	require.Equal(t, commit, first.before)
	require.Equal(t, map[uint64]struct{}{1: {}}, first.entries)
	report := nextReport(t, adapter)
	require.Equal(t, RestoredPartReport{Records: 1, Committed: commit, Late: 2, WorstLate: 3 * time.Second, LeastLate: time.Second, LockWait: time.Millisecond, CommitHold: 2 * time.Millisecond}, report.Parts[0])
	require.Equal(t, RestoredPartReport{Records: 1, Committed: commit, LockWait: time.Millisecond, CommitHold: 2 * time.Millisecond}, report.Parts[1])
}

// A restore whose connection ends stops: what merged stays, the file is
// reported with why it stopped, and no answer goes.
func TestRestoreStopsWithItsConnection(t *testing.T) {
	t.Parallel()
	adapter := newFakeAdapter()
	adapter.restore = func(ctx context.Context, part dagql.ValueBundle, _ *dagql.KnownRows) (dagql.MergeReply, error) {
		if part.Values[0].SenderNumber == 2 {
			<-ctx.Done()
			return dagql.MergeReply{}, context.Cause(ctx)
		}
		return merged(part, time.Now()), nil
	}
	s, conn := restoreSession(t, adapter)
	files := newFileServer(t)
	whole := files.add("whole", setFile(t, dagql.ValueBundleVersion, part(1), part(2), part(3)))
	queued := files.add("queued", setFile(t, dagql.ValueBundleVersion, part(4)))
	s.dispatch(request(t, 1, protocol.TypeRestore, restoreOf("c", nil, &whole)))
	s.dispatch(request(t, 2, protocol.TypeRestore, restoreOf("c", nil, &queued)))
	require.Equal(t, []uint64{1, 2}, seen(t, adapter, 2))
	s.cancel(errors.New("gone"))
	stopped := nextReport(t, adapter)
	require.Equal(t, whole.Digest, stopped.Digest)
	require.Len(t, stopped.Parts, 1, "the part that merged stays")
	require.False(t, stopped.Complete)
	left := nextReport(t, adapter)
	require.Equal(t, queued.Digest, left.Digest)
	require.Equal(t, errRestoreConnectionEnded.Error(), left.Error)
	conn.none(t)
	require.False(t, s.cfg.restored.has(whole.Digest))
}
