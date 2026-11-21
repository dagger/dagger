package remotecache

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime/metrics"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
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
	reply := dagql.MergeReply{Committed: true, EngineTimeUnixNano: at.UnixNano(), Rows: dagql.MergeRowCounts{Created: len(part.Values)}, LockWait: time.Millisecond, CommitHold: 2 * time.Millisecond, Prepare: 3 * time.Millisecond, Commit: 4 * time.Millisecond}
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
		Entries:     protocol.EntryNumbers{11, 12},
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
	require.Equal(t, 6*time.Millisecond, report.Prepare, "the parts' preparations, in all")
	require.Equal(t, 8*time.Millisecond, report.Commit, "and their commits")
	require.Positive(t, report.Decode, "and their decodes")
	require.Equal(t, "test-base whole "+whole.Digest.String(), <-adapter.sourceSeen, "each part's merge names its file")
	require.False(t, report.FetchStarted.IsZero())
	require.False(t, report.Fetched.Before(report.FetchStarted))
	require.Equal(t, 1, files.getsOf("whole"))
}

// A file's next part is decoded while the part before it merges, so that a
// part's merge doesn't wait for its decode after the part before has
// committed; the part's report says how long its merge did wait for it.
func TestRestoreDecodesTheNextPartWhileAPartMerges(t *testing.T) {
	t.Parallel()
	adapter := newFakeAdapter()
	s, conn := restoreSession(t, adapter)
	decoded := make(chan uint64, 3)
	s.cfg.decode = func(data []byte) (dagql.ValueBundle, error) {
		bundle, err := decodePart(data)
		if err == nil {
			decoded <- bundle.Values[0].SenderNumber
		}
		return bundle, err
	}
	adapter.restore = func(_ context.Context, part dagql.ValueBundle, _ *dagql.KnownRows) (dagql.MergeReply, error) {
		if part.Values[0].SenderNumber == 11 {
			for {
				select {
				case n := <-decoded:
					if n == 12 {
						return merged(part, time.Now()), nil
					}
				case <-time.After(5 * time.Second):
					return dagql.MergeReply{}, errors.New("part 12 wasn't decoded while part 11 merged")
				}
			}
		}
		return merged(part, time.Now()), nil
	}
	files := newFileServer(t)
	whole := files.add("whole", setFile(t, dagql.ValueBundleVersion, part(11), part(12), part(13)))

	s.dispatch(request(t, 1, protocol.TypeRestore, restoreOf("test-base", nil, &whole)))
	restored := decode[protocol.Restored](t, conn.next(t))
	require.Len(t, restored.Files, 1)
	require.Empty(t, restored.Files[0].Error)
	require.True(t, restored.Files[0].Complete)
	report := nextReport(t, adapter)
	require.Len(t, report.Parts, 3)
	for i, p := range report.Parts {
		require.Positive(t, p.Decode, "part %d's decode", i)
		require.GreaterOrEqual(t, p.DecodeWait, time.Duration(0), "part %d's wait for it", i)
	}
}

// A file's next part is checked for its merge (Adapter.CheckRestorePart)
// while the part before it merges, after its decode, so that a part's merge
// doesn't wait for the half of its preparation that reads nothing of the
// cache; the part's report says how long the check took.
func TestRestoreChecksTheNextPartWhileAPartMerges(t *testing.T) {
	t.Parallel()
	adapter := newFakeAdapter()
	checked := make(chan uint64, 3)
	adapter.check = func(part dagql.ValueBundle) error {
		checked <- part.Values[0].SenderNumber
		return nil
	}
	adapter.restore = func(_ context.Context, part dagql.ValueBundle, _ *dagql.KnownRows) (dagql.MergeReply, error) {
		if part.Values[0].SenderNumber == 11 {
			for {
				select {
				case n := <-checked:
					if n == 12 {
						return merged(part, time.Now()), nil
					}
				case <-time.After(5 * time.Second):
					return dagql.MergeReply{}, errors.New("part 12 wasn't checked while part 11 merged")
				}
			}
		}
		return merged(part, time.Now()), nil
	}
	s, conn := restoreSession(t, adapter)
	files := newFileServer(t)
	whole := files.add("whole", setFile(t, dagql.ValueBundleVersion, part(11), part(12), part(13)))

	s.dispatch(request(t, 1, protocol.TypeRestore, restoreOf("test-base", nil, &whole)))
	restored := decode[protocol.Restored](t, conn.next(t))
	require.Len(t, restored.Files, 1)
	require.Empty(t, restored.Files[0].Error)
	require.True(t, restored.Files[0].Complete)
	report := nextReport(t, adapter)
	require.Len(t, report.Parts, 3)
}

// A part the adapter's check refuses stops its file, as a refused merge
// does, with what merged of it before.
func TestRestoreStopsAtAPartItsCheckRefuses(t *testing.T) {
	t.Parallel()
	adapter := newFakeAdapter()
	adapter.check = func(part dagql.ValueBundle) error {
		if part.Values[0].SenderNumber == 12 {
			return errors.New("refused")
		}
		return nil
	}
	adapter.restore = func(_ context.Context, part dagql.ValueBundle, _ *dagql.KnownRows) (dagql.MergeReply, error) {
		return merged(part, time.Now()), nil
	}
	s, conn := restoreSession(t, adapter)
	files := newFileServer(t)
	whole := files.add("whole", setFile(t, dagql.ValueBundleVersion, part(11), part(12), part(13)))

	s.dispatch(request(t, 1, protocol.TypeRestore, restoreOf("test-base", nil, &whole)))
	restored := decode[protocol.Restored](t, conn.next(t))
	require.Len(t, restored.Files, 1)
	require.False(t, restored.Files[0].Complete)
	require.Equal(t, "part 1: refused", restored.Files[0].Error)
	require.Equal(t, 1, restored.Files[0].Parts)
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

// Heads merge on a lane of their own, and each whole on a lane of its own:
// two claims' heads, offered together with their wholes, both commit while
// the first whole is still merging, and so does the second whole, and the
// head of a claim that arrives meanwhile. Heads keep their arrival order.
func TestRestoreMergesHeadsBesideWholes(t *testing.T) {
	t.Parallel()
	adapter := newFakeAdapter()
	release := make(chan struct{})
	var released sync.Once
	adapter.restore = func(_ context.Context, part dagql.ValueBundle, _ *dagql.KnownRows) (dagql.MergeReply, error) {
		if part.Values[0].SenderNumber == 101 {
			<-release
		}
		return merged(part, time.Now()), nil
	}
	s, conn := restoreSession(t, adapter)
	// A failing check leaves no merge waiting, before the session's
	// cleanup waits for its merges.
	t.Cleanup(func() { released.Do(func() { close(release) }) })
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
	got := seen(t, adapter, 4)
	require.ElementsMatch(t, []uint64{1, 2, 101, 201}, got, "both heads, and wholeB, merge while wholeA's first part is merging")
	require.Less(t, slices.Index(got, 1), slices.Index(got, 2), "heads keep their order")
	answered := []uint64{conn.next(t).Re, conn.next(t).Re, conn.next(t).Re}
	require.ElementsMatch(t, []uint64{1, 3, 4}, answered)

	// A claim that arrives while wholeA is still merging.
	s.dispatch(request(t, 5, protocol.TypeRestore, restoreOf("c", &headC, nil)))
	require.Equal(t, []uint64{3, 4}, seen(t, adapter, 2))
	require.Equal(t, uint64(5), conn.next(t).Re)
	conn.none(t)

	released.Do(func() { close(release) })
	require.Equal(t, []uint64{102}, seen(t, adapter, 1))
	require.Equal(t, uint64(2), conn.next(t).Re)
}

// Two wholes whose parts merge in turn, one of each file after the other,
// each answer with their own records' entries, complete: a whole's reply is
// built from its own parts' merges alone, on its own lane.
func TestRestoreRepliesWithEachWholesOwnEntriesWhenTheyInterleave(t *testing.T) {
	t.Parallel()
	adapter := newFakeAdapter()
	// Each part merges once the one before it in turn has.
	turns := []uint64{11, 21, 12, 22}
	done := map[uint64]chan struct{}{}
	for _, sender := range turns {
		done[sender] = make(chan struct{})
	}
	var mu sync.Mutex
	var order []uint64
	adapter.restore = func(_ context.Context, part dagql.ValueBundle, _ *dagql.KnownRows) (dagql.MergeReply, error) {
		sender := part.Values[0].SenderNumber
		if i := slices.Index(turns, sender); i > 0 {
			select {
			case <-done[turns[i-1]]:
			case <-time.After(10 * time.Second):
				return dagql.MergeReply{}, fmt.Errorf("part %d's turn never came", sender)
			}
		}
		defer close(done[sender])
		mu.Lock()
		order = append(order, sender)
		mu.Unlock()
		return merged(part, time.Now()), nil
	}
	s, conn := restoreSession(t, adapter)
	files := newFileServer(t)
	wholeA := files.add("wholeA", setFile(t, dagql.ValueBundleVersion, part(11), part(12)))
	wholeB := files.add("wholeB", setFile(t, dagql.ValueBundleVersion, part(21), part(22)))
	s.dispatch(request(t, 1, protocol.TypeRestore, restoreOf("a", nil, &wholeA)))
	s.dispatch(request(t, 2, protocol.TypeRestore, restoreOf("b", nil, &wholeB)))
	replies := map[uint64]protocol.RestoredFile{}
	for range 2 {
		reply := conn.next(t)
		replies[reply.Re] = decode[protocol.Restored](t, reply).Files[0]
	}
	mu.Lock()
	require.Equal(t, turns, order, "the files' parts merged in turn")
	mu.Unlock()
	for re, want := range map[uint64]protocol.EntryNumbers{1: {11, 12}, 2: {21, 22}} {
		file := replies[re]
		require.True(t, file.Complete, "restore %d is complete", re)
		require.Equal(t, 2, file.Parts)
		require.Equal(t, want, file.Entries, "restore %d names its own records' entries", re)
	}
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
			s.dispatch(request(t, 2, protocol.TypeRestore, restoreOf("d", nil, &next)))
			// Each whole merges on a lane of its own, so either may end
			// first.
			replies := map[uint64]protocol.Envelope{}
			for range 2 {
				reply := conn.next(t)
				replies[reply.Re] = reply
			}
			reply := replies[1]
			require.Equal(t, protocol.TypeRestored, reply.Type, "a failed file is answered, not refused")
			file := decode[protocol.Restored](t, reply).Files[0]
			require.False(t, file.Complete)
			require.Equal(t, tc.parts, file.Parts)
			require.Contains(t, file.Error, tc.err)
			reports := map[digest.Digest]RestoreReport{}
			for range 2 {
				report := nextReport(t, adapter)
				reports[report.Digest] = report
			}
			report := reports[bad.Digest]
			require.Equal(t, file.Error, report.Error)
			require.Len(t, report.Parts, tc.parts)

			require.True(t, decode[protocol.Restored](t, replies[2]).Files[0].Complete, "the next restore goes on")
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
// download, on this connection or a later one, with the entries its merge
// answered, and each hello names it.
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
	require.Equal(t, protocol.RestoredFile{Digest: file.Digest, Complete: true, Already: true, Entries: protocol.EntryNumbers{1}}, again)
	require.True(t, nextReport(t, adapter).Already)
	require.Equal(t, 1, files.getsOf("whole"))

	// A later connection of the same process says so in its hello.
	next := newSession(t.Context(), newRecordingConn(), adapter, s.cfg)
	t.Cleanup(next.close)
	go func() { _, _ = next.serve() }()
	hello := next.conn.(*recordingConn).next(t)
	require.Equal(t, []digest.Digest{file.Digest}, decode[protocol.Hello](t, hello).Restored)
}

// A file this engine process merged whole, offered again on a later
// connection, is answered already, with the entries and complete parts its
// first merge noted, and isn't fetched or merged again. The service offers
// such a file again only when it never read the first answer, as when the
// connection dropped before it did: without the entries it can't hold the
// file's records by file, and holdings built on them look incomplete.
func TestRestoreOfAFileMergedWholeOnAnEarlierConnectionAnswersItsEntries(t *testing.T) {
	t.Parallel()
	adapter := newFakeAdapter()
	adapter.restore = func(_ context.Context, part dagql.ValueBundle, _ *dagql.KnownRows) (dagql.MergeReply, error) {
		reply := merged(part, time.Now())
		for i := range reply.Values {
			reply.Values[i].Number *= 10
			if part.Values[0].SenderNumber == 2 {
				reply.Values[i].Parts = []dagql.PersistedPartAddress{{Part: "metadata"}}
			}
		}
		return reply, nil
	}
	s, conn := restoreSession(t, adapter)
	files := newFileServer(t)
	file := files.add("whole", setFile(t, dagql.ValueBundleVersion, part(1), part(2)))
	s.dispatch(request(t, 1, protocol.TypeRestore, restoreOf("c", nil, &file)))
	first := decode[protocol.Restored](t, conn.next(t)).Files[0]
	require.True(t, first.Complete)
	require.Equal(t, protocol.EntryNumbers{10, 20}, first.Entries)
	nextReport(t, adapter)

	// The connection drops before the service reads that answer. The same
	// process connects again, and is offered the file again.
	s.close()
	nextConn := newRecordingConn()
	next := newSession(t.Context(), nextConn, adapter, s.cfg)
	t.Cleanup(next.close)
	next.go_(next.restorer.run)
	next.dispatch(request(t, 1, protocol.TypeRestore, restoreOf("c", nil, &file)))
	again := decode[protocol.Restored](t, nextConn.next(t)).Files[0]
	require.True(t, again.Complete)
	require.True(t, again.Already)
	require.Equal(t, first.Entries, again.Entries, "the entries its first merge noted")
	require.Equal(t, first.PartsTable, again.PartsTable)
	require.Equal(t, first.CompleteParts, again.CompleteParts, "and their complete parts")
	complete, err := protocol.UnpackCompleteParts(again.PartsTable, again.CompleteParts)
	require.NoError(t, err)
	require.Equal(t, map[int][]dagql.PersistedPartAddress{1: {{Part: "metadata"}}}, complete)
	require.Zero(t, again.Parts, "nothing merged this time")
	require.Zero(t, again.Records)
	require.True(t, nextReport(t, adapter).Already)
	require.Equal(t, 1, files.getsOf("whole"), "not fetched again")
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
	for i := range report.Parts {
		require.Positive(t, report.Parts[i].Decode)
		require.GreaterOrEqual(t, report.Parts[i].DecodeWait, time.Duration(0))
		report.Parts[i].Decode, report.Parts[i].DecodeWait = 0, 0
	}
	require.Equal(t, RestoredPartReport{Records: 1, Committed: commit, Late: 2, WorstLate: 3 * time.Second, LeastLate: time.Second, LockWait: time.Millisecond, CommitHold: 2 * time.Millisecond, Prepare: 3 * time.Millisecond, Commit: 4 * time.Millisecond}, report.Parts[0])
	require.Equal(t, RestoredPartReport{Records: 1, Committed: commit, LockWait: time.Millisecond, CommitHold: 2 * time.Millisecond, Prepare: 3 * time.Millisecond, Commit: 4 * time.Millisecond}, report.Parts[1])
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
	// Another whole, still downloading when the connection ends.
	queued := files.add("queued", setFile(t, dagql.ValueBundleVersion, part(4)))
	files.gate("queued")
	s.dispatch(request(t, 1, protocol.TypeRestore, restoreOf("c", nil, &whole)))
	s.dispatch(request(t, 2, protocol.TypeRestore, restoreOf("d", nil, &queued)))
	require.Equal(t, []uint64{1, 2}, seen(t, adapter, 2))
	s.cancel(errors.New("gone"))
	reports := map[digest.Digest]RestoreReport{}
	for range 2 {
		report := nextReport(t, adapter)
		reports[report.Digest] = report
	}
	stopped := reports[whole.Digest]
	require.Len(t, stopped.Parts, 1, "the part that merged stays")
	require.False(t, stopped.Complete)
	require.NotNil(t, stopped.Window, "a file that ended has its window")
	left := reports[queued.Digest]
	require.Equal(t, errRestoreConnectionEnded.Error(), left.Error)
	require.Nil(t, left.Window, "a file left as its connection ended has none")
	require.False(t, left.Ended.Before(stopped.Ended), "and ended after")
	conn.none(t)
	require.NotContains(t, s.cfg.restored.list(), whole.Digest, "a later hello doesn't name it merged")
	require.Zero(t, adapter.landing.Load(), "no whole is left landing")
	// A restore that arrives once the connection's end abandoned the files
	// left is queued no more: nothing would end its whole's landing.
	late := files.add("late", setFile(t, dagql.ValueBundleVersion, part(5)))
	s.restorer.handleRestore(request(t, 3, protocol.TypeRestore, restoreOf("e", nil, &late)))
	require.Zero(t, adapter.landing.Load(), "no whole is left landing")
}

// setFileOf is a set file of the parts with their header entries' unretained
// roots, as the service cuts a root's closure into parts of its own.
func setFileOf(t *testing.T, parts []dagql.ValueBundle, unretained [][]dagql.TransferOrdinal) []byte {
	t.Helper()
	enc, err := zstd.NewWriter(nil)
	require.NoError(t, err)
	defer enc.Close()
	header := protocol.SetFileHeader{Version: dagql.ValueBundleVersion}
	var raw [][]byte
	for i, part := range parts {
		data, entry, err := protocol.EncodeSetFilePart(enc, part)
		require.NoError(t, err)
		entry.Unretained = unretained[i]
		raw = append(raw, data)
		header.Parts = append(header.Parts, entry)
	}
	var buf bytes.Buffer
	require.NoError(t, protocol.WriteSetFile(&buf, header, raw))
	return buf.Bytes()
}

// Each part is merged with the roots its header entry leaves unretained,
// and the hold each committed part's merge took is released only when the
// file ends: after its last part, whether the file completed, a part
// stopped it, or the connection ended, so that a later part's held rows
// find an earlier part's records.
func TestRestoreHoldsEachPartUntilTheFileEnds(t *testing.T) {
	t.Parallel()
	for _, end := range []string{"completed", "stopped", "disconnected"} {
		t.Run(end, func(t *testing.T) {
			t.Parallel()
			adapter := newFakeAdapter()
			var released atomic.Int32
			second := make(chan struct{})
			adapter.restore = func(ctx context.Context, part dagql.ValueBundle, _ *dagql.KnownRows) (dagql.MergeReply, error) {
				if part.Values[0].SenderNumber == 2 {
					switch end {
					case "stopped":
						<-second
						return dagql.MergeReply{}, errors.New("refused: a held row is stale")
					case "disconnected":
						<-ctx.Done()
						return dagql.MergeReply{}, context.Cause(ctx)
					}
					<-second
				}
				reply := merged(part, time.Now())
				reply.Release = func(context.Context) error {
					released.Add(1)
					return nil
				}
				return reply, nil
			}
			s, conn := restoreSession(t, adapter)
			files := newFileServer(t)
			whole := files.add("whole", setFileOf(t, []dagql.ValueBundle{part(1), part(2)}, [][]dagql.TransferOrdinal{{1}, nil}))
			s.dispatch(request(t, 1, protocol.TypeRestore, restoreOf("c", nil, &whole)))

			require.Equal(t, []dagql.TransferOrdinal{1}, <-adapter.unretainedSeen, "the first part's root is merged unretained")
			require.Empty(t, <-adapter.unretainedSeen)
			require.Equal(t, []uint64{1, 2}, seen(t, adapter, 2))
			require.Zero(t, released.Load(), "the first part stays held while the second merges")
			if end == "disconnected" {
				s.cancel(errors.New("gone"))
			} else {
				close(second)
				file := decode[protocol.Restored](t, conn.next(t)).Files[0]
				require.Equal(t, end == "completed", file.Complete)
			}
			nextReport(t, adapter)
			want := int32(2)
			if end != "completed" {
				want = 1
			}
			require.Equal(t, want, released.Load(), "every committed part's hold is released when the file ends")
		})
	}
}

// A file's restore line says how its download began: how long the GET
// waited for a connection, whether that connection was new, and its time to
// the response's first byte. A head fetched in 1.1 s where its engine's
// other heads took 21 ms made a check miss its own results, and the line
// gave the download's total alone.
func TestRestoreLogsHowItsDownloadBegan(t *testing.T) {
	t.Parallel()
	adapter := newFakeAdapter()
	adapter.restore = func(_ context.Context, part dagql.ValueBundle, _ *dagql.KnownRows) (dagql.MergeReply, error) {
		return merged(part, time.Now()), nil
	}
	s, conn, logs := loggedSession(t, adapter)
	s.cfg.peakRSS = func() int64 { return 0 }
	// A transport of its own: the default one's idle connections are
	// shared with every test, which can close this one's.
	transport := &http.Transport{}
	t.Cleanup(transport.CloseIdleConnections)
	s.cfg.getFile = getSetFile(&http.Client{Transport: transport}, restoreIdleTimeout)
	s.go_(s.restorer.run)
	files := newFileServer(t)
	slow := files.add("slow", setFile(t, dagql.ValueBundleVersion, part(1)))
	gate := files.gate("slow")
	s.dispatch(request(t, 1, protocol.TypeRestore, restoreOf("c", &slow, nil)))
	// The store holds its first byte from the GET's arrival for held, so
	// the first byte comes at least held after the GET began.
	require.Eventually(t, func() bool { return files.getsOf("slow") == 1 }, 10*time.Second, time.Millisecond)
	const held = 200 * time.Millisecond
	time.Sleep(held)
	close(gate)
	require.True(t, decode[protocol.Restored](t, conn.next(t)).Files[0].Complete)

	fast := files.add("fast", setFile(t, dagql.ValueBundleVersion, part(2)))
	s.dispatch(request(t, 2, protocol.TypeRestore, restoreOf("d", &fast, nil)))
	require.True(t, decode[protocol.Restored](t, conn.next(t)).Files[0].Complete)

	lines := logs.waitLines(t, "remote cache restore", 2)
	byCheck := map[string]map[string]any{}
	for _, line := range lines {
		byCheck[line["check"].(string)] = line
	}
	first, second := byCheck["c"], byCheck["d"]
	require.GreaterOrEqual(t, logDuration(t, first, "fetch_ttfb"), held, "the first byte waited for the store")
	require.LessOrEqual(t, logDuration(t, first, "fetch_ttfb"), logDuration(t, first, "fetch"))
	require.Equal(t, true, first["fetch_new_conn"], "the store's first GET opens a connection")
	require.Equal(t, false, second["fetch_new_conn"], "the next GET reuses it")
	require.LessOrEqual(t, logDuration(t, second, "fetch_conn"), logDuration(t, second, "fetch_ttfb"))
}

// A complete file's reply names the entry each record it carries in full
// landed on, in the file's order: part by part, each record at its first
// appearance, held rows left out, and 0 for a record that landed nowhere;
// and the parts each of those entries had complete, by its place. A file
// that didn't complete names none.
func TestRestoreRepliesWithEachRecordsEntry(t *testing.T) {
	t.Parallel()
	first := dagql.ValueBundle{Version: dagql.ValueBundleVersion, Roots: []dagql.TransferredRoot{{Ordinal: 1}}, Values: []dagql.TransferredValue{
		{Ordinal: 1, SenderNumber: 12},
		{Ordinal: 2, SenderNumber: 11},
	}}
	second := dagql.ValueBundle{Version: dagql.ValueBundleVersion, Roots: []dagql.TransferredRoot{{Ordinal: 1}}, Values: []dagql.TransferredValue{
		{Ordinal: 1, SenderNumber: 13},
		{Ordinal: 2, SenderNumber: 11},
		{Ordinal: 3, SenderNumber: 14, Held: "xxh3:14"},
		{Ordinal: 4, SenderNumber: 15},
	}}
	for _, refuse := range []bool{false, true} {
		t.Run(fmt.Sprint("refused=", refuse), func(t *testing.T) {
			t.Parallel()
			adapter := newFakeAdapter()
			adapter.restore = func(_ context.Context, part dagql.ValueBundle, _ *dagql.KnownRows) (dagql.MergeReply, error) {
				if refuse && part.Values[0].SenderNumber == 13 {
					return dagql.MergeReply{}, errors.New("refused")
				}
				reply := dagql.MergeReply{Committed: true, EngineTimeUnixNano: time.Now().UnixNano()}
				for _, value := range part.Values {
					if value.SenderNumber == 15 {
						continue // landed nowhere
					}
					merged := dagql.MergedValue{Ordinal: value.Ordinal, Number: value.SenderNumber * 10}
					if value.SenderNumber == 11 {
						merged.Parts = []dagql.PersistedPartAddress{{Part: "metadata"}, {Part: "execMeta"}}
					}
					if value.SenderNumber == 13 {
						merged.Parts = []dagql.PersistedPartAddress{{Part: "metadata"}}
					}
					reply.Values = append(reply.Values, merged)
				}
				// A merge answers in its own order, not the bundle's.
				slices.Reverse(reply.Values)
				return reply, nil
			}
			s, conn := restoreSession(t, adapter)
			files := newFileServer(t)
			whole := files.add("whole", setFile(t, dagql.ValueBundleVersion, first, second))
			s.dispatch(request(t, 1, protocol.TypeRestore, restoreOf("c", nil, &whole)))
			file := decode[protocol.Restored](t, conn.next(t)).Files[0]
			if refuse {
				require.False(t, file.Complete)
				require.Empty(t, file.Entries, "a file that didn't complete names no entries")
				require.Empty(t, file.CompleteParts)
				return
			}
			require.True(t, file.Complete)
			require.Equal(t, protocol.EntryNumbers{120, 110, 130, 0}, file.Entries)
			complete, err := protocol.UnpackCompleteParts(file.PartsTable, file.CompleteParts)
			require.NoError(t, err)
			require.Equal(t, map[int][]dagql.PersistedPartAddress{
				1: {{Part: "metadata"}, {Part: "execMeta"}},
				2: {{Part: "metadata"}},
			}, complete, "each entry's complete parts, at the place of its entry")
		})
	}
}

// A restore reports each part's own stages: its decode, the preparation and
// commit of its merge, and its merge's wait behind the merges that answer
// lookups. Each file's report has its window: what the engine did from the
// file's arrival to its end, the cache's counts among it. A whole is marked
// landing on the cache from its arrival to its end; a head isn't.
func TestRestoreReportsEachPartsStagesAndItsWindow(t *testing.T) {
	t.Parallel()
	adapter := newFakeAdapter()
	adapter.priorityWait = 5 * time.Millisecond
	var reads atomic.Int64
	adapter.work = func() CacheWork {
		n := reads.Add(1)
		return CacheWork{
			LookupLockWaits: dagql.LookupLockWaits{Landing: map[string]dagql.LockWaitStats{"sharing": {Waits: n, Total: time.Duration(n) * time.Millisecond}}},
			FirstUse:        dagql.FirstUseStats{Paths: map[string]dagql.FirstUsePath{"sharing": {Prepared: 10 * n, Total: time.Duration(n) * time.Second}}},
			Sharing:         dagql.SharingStats{Kinds: map[string]dagql.SharePassStats{"alone": {Passes: 2 * n, Members: 2 * n, Prepared: 10 * n, PrepareTotal: time.Duration(n) * time.Second}}},
		}
	}
	landing := make(chan int64, 8)
	adapter.restore = func(_ context.Context, part dagql.ValueBundle, _ *dagql.KnownRows) (dagql.MergeReply, error) {
		landing <- adapter.landing.Load()
		reply := merged(part, time.Now())
		reply.PrepareLockWait, reply.PrepareLockWaits = 6*time.Millisecond, map[string]time.Duration{"restore": 6 * time.Millisecond}
		return reply, nil
	}
	s, _ := restoreSession(t, adapter)
	files := newFileServer(t)
	head := files.add("head", setFile(t, dagql.ValueBundleVersion, part(10)))
	whole := files.add("whole", setFile(t, dagql.ValueBundleVersion, part(11), part(12)))

	s.dispatch(request(t, 7, protocol.TypeRestore, restoreOf("test-base", &head, nil)))
	require.Equal(t, int64(0), <-landing, "a head isn't marked landing")
	headReport := nextReport(t, adapter)
	require.NotNil(t, headReport.Window, "a head has its window")
	s.dispatch(request(t, 8, protocol.TypeRestore, restoreOf("test-base", nil, &whole)))
	require.Equal(t, int64(1), <-landing, "a whole is marked landing as it merges")
	require.Equal(t, int64(1), <-landing)
	report := nextReport(t, adapter)
	require.Zero(t, adapter.landing.Load(), "and no longer once it ended")

	require.Len(t, report.Parts, 2)
	for _, p := range report.Parts {
		require.Positive(t, p.Decode, "the part's own decode")
		require.Equal(t, 3*time.Millisecond, p.Prepare, "its merge's preparation")
		require.Equal(t, 4*time.Millisecond, p.Commit, "and commit")
		require.Equal(t, 5*time.Millisecond, p.PriorityWait, "and its wait behind lookup merges")
		require.Equal(t, 6*time.Millisecond, p.PrepareLockWait, "and its preparation's wait for the cache's lock")
		require.Equal(t, map[string]time.Duration{"restore": 6 * time.Millisecond}, p.PrepareLockWaits, "by who let it in")
	}
	require.Equal(t, report.Parts[0].Decode+report.Parts[1].Decode, report.Decode)

	w := report.Window
	require.NotNil(t, w)
	require.GreaterOrEqual(t, w.Wall, report.Fetched.Sub(report.FetchStarted), "the window spans the download")
	require.GreaterOrEqual(t, w.CPU, time.Duration(0))
	waits := w.LookupLockWaits["sharing"].Waits
	require.Positive(t, waits, "the cache's counts grew between the window's readings")
	require.Equal(t, map[string]dagql.LockWaitStats{"sharing": {Waits: waits, Total: time.Duration(waits) * time.Millisecond}}, w.LookupLockWaits)
	require.Equal(t, map[string]dagql.FirstUsePath{"sharing": {Prepared: 10 * waits, Total: time.Duration(waits) * time.Second}}, w.FirstUses)
	require.Equal(t, map[string]dagql.SharePassStats{"alone": {Passes: 2 * waits, Members: 2 * waits, Prepared: 10 * waits, PrepareTotal: time.Duration(waits) * time.Second}}, w.Sharing)
}

// A window's goroutine waits are the scheduler latency samples the runtime
// took in it, their time estimated from each bucket's middle, or its finite
// edge for an unbounded bucket.
func TestRestoreWindowEstimatesSchedulerWaits(t *testing.T) {
	t.Parallel()
	buckets := []float64{math.Inf(-1), 0, 0.001, 0.003, math.Inf(1)}
	start := &metrics.Float64Histogram{Buckets: buckets, Counts: []uint64{0, 5, 1, 0}}
	end := &metrics.Float64Histogram{Buckets: buckets, Counts: []uint64{0, 7, 3, 1}}
	n, total := histogramSince(end, start)
	require.Equal(t, int64(5), n)
	require.InDelta(t, float64(2*500*time.Microsecond+2*2*time.Millisecond+3*time.Millisecond), float64(total), float64(time.Microsecond))
	n, total = histogramSince(end, nil)
	require.Zero(t, n)
	require.Zero(t, total)
}

// The engine's threads' wait for a CPU is the sum of each thread's, the
// second field of its schedstat, over the threads whose counts read.
func TestRestoreWindowSumsThreadsRunqWait(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for task, stat := range map[string]string{
		"1":  "9000 1500 12\n",
		"7":  "100 250 3\n",
		"9":  "garbled\n",
		"12": "",
	} {
		require.NoError(t, os.MkdirAll(filepath.Join(dir, task), 0o755))
		if stat != "" {
			require.NoError(t, os.WriteFile(filepath.Join(dir, task, "schedstat"), []byte(stat), 0o644))
		}
	}
	waited, ok := threadsRunqWait(dir)
	require.True(t, ok)
	require.Equal(t, 1750*time.Nanosecond, waited)
	_, ok = threadsRunqWait(filepath.Join(dir, "missing"))
	require.False(t, ok)
}
