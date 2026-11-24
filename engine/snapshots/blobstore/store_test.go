package blobstore

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/leases"
	"github.com/containerd/containerd/v2/core/metadata"
	ctdsnapshots "github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/errdefs"
	"github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
	bolt "go.etcd.io/bbolt"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(t.TempDir())
	require.NoError(t, err)
	return s
}

func descOf(data string) ocispecs.Descriptor {
	return ocispecs.Descriptor{Digest: digest.FromString(data), Size: int64(len(data))}
}

func write(t *testing.T, s content.Ingester, ref, data string) error {
	t.Helper()
	desc := descOf(data)
	return content.WriteBlob(t.Context(), s, ref, strings.NewReader(data), desc)
}

func read(t *testing.T, s content.Provider, desc ocispecs.Descriptor) string {
	t.Helper()
	got, err := content.ReadBlob(t.Context(), s, desc)
	require.NoError(t, err)
	return string(got)
}

// A committed write is the blob its digest names: Info, ReaderAt and Walk
// find it, and its write is gone.
func TestWriteAndRead(t *testing.T) {
	s := newStore(t)
	require.NoError(t, write(t, s, "a", "hello"))
	desc := descOf("hello")
	info, err := s.Info(t.Context(), desc.Digest)
	require.NoError(t, err)
	require.Equal(t, desc.Digest, info.Digest)
	require.Equal(t, int64(5), info.Size)
	require.Equal(t, "hello", read(t, s, desc))

	var walked []digest.Digest
	require.NoError(t, s.Walk(t.Context(), func(info content.Info) error {
		walked = append(walked, info.Digest)
		return nil
	}))
	require.Equal(t, []digest.Digest{desc.Digest}, walked)

	_, err = s.Status(t.Context(), "a")
	require.True(t, errdefs.IsNotFound(err), "a committed write leaves no write behind: %v", err)
}

// A write whose bytes aren't the descriptor's, in size or digest, stores
// nothing.
func TestCommitChecksSizeAndDigest(t *testing.T) {
	s := newStore(t)
	desc := descOf("hello")
	w, err := s.Writer(t.Context(), content.WithRef("short"), content.WithDescriptor(desc))
	require.NoError(t, err)
	_, err = w.Write([]byte("hell"))
	require.NoError(t, err)
	err = w.Commit(t.Context(), desc.Size, desc.Digest)
	require.True(t, errdefs.IsFailedPrecondition(err), "%v", err)

	w, err = s.Writer(t.Context(), content.WithRef("wrong"), content.WithDescriptor(desc))
	require.NoError(t, err)
	_, err = w.Write([]byte("jello"))
	require.NoError(t, err)
	err = w.Commit(t.Context(), desc.Size, desc.Digest)
	require.True(t, errdefs.IsFailedPrecondition(err), "%v", err)

	_, err = s.Info(t.Context(), desc.Digest)
	require.True(t, errdefs.IsNotFound(err), "%v", err)
}

// A blob already stored is never written again: opening a write for its
// digest fails as already existing, and so does committing a second write
// of the same bytes, which leaves the stored blob and no write behind.
func TestDedupeByDigest(t *testing.T) {
	s := newStore(t)
	desc := descOf("hello")
	second, err := s.Writer(t.Context(), content.WithRef("second"))
	require.NoError(t, err)
	require.NoError(t, write(t, s, "first", "hello"))

	_, err = s.Writer(t.Context(), content.WithRef("third"), content.WithDescriptor(desc))
	require.True(t, errdefs.IsAlreadyExists(err), "%v", err)

	_, err = second.Write([]byte("hello"))
	require.NoError(t, err)
	err = second.Commit(t.Context(), desc.Size, desc.Digest)
	require.True(t, errdefs.IsAlreadyExists(err), "%v", err)
	_, err = s.Status(t.Context(), "second")
	require.True(t, errdefs.IsNotFound(err), "%v", err)
	require.Equal(t, "hello", read(t, s, desc))
}

// One writer holds a ref at a time: another is unavailable until the first
// closes, which content.OpenWriter waits out. Reopening a write starts it
// over.
func TestOneWriterPerRef(t *testing.T) {
	s := newStore(t)
	w, err := s.Writer(t.Context(), content.WithRef("layer"))
	require.NoError(t, err)
	_, err = w.Write([]byte("partial"))
	require.NoError(t, err)

	_, err = s.Writer(t.Context(), content.WithRef("layer"))
	require.True(t, errdefs.IsUnavailable(err), "%v", err)
	require.NoError(t, w.Close())

	st, err := s.Status(t.Context(), "layer")
	require.NoError(t, err)
	require.Equal(t, int64(7), st.Offset, "a closed write stays until it's aborted")

	require.NoError(t, write(t, s, "layer", "hello"))
	require.Equal(t, "hello", read(t, s, descOf("hello")))
}

// A blob stored at another size than the descriptor's, as a host crash can
// leave it, fails to open as lost data.
func TestReaderAtChecksSize(t *testing.T) {
	s := newStore(t)
	require.NoError(t, write(t, s, "a", "hello"))
	desc := descOf("hello")
	p, err := s.blobPath(desc.Digest)
	require.NoError(t, err)
	require.NoError(t, os.Truncate(p, 3))

	_, err = s.ReaderAt(t.Context(), desc)
	require.True(t, errdefs.IsDataLoss(err), "%v", err)
}

// Writes left behind are listed by ref and removed by Abort.
func TestStatusesAndAbort(t *testing.T) {
	s := newStore(t)
	for _, ref := range []string{"left", "open"} {
		w, err := s.Writer(t.Context(), content.WithRef(ref))
		require.NoError(t, err)
		_, err = w.Write([]byte(ref))
		require.NoError(t, err)
		if ref == "left" {
			require.NoError(t, w.Close())
		}
	}
	var refs []string
	require.NoError(t, s.WalkStatusRefs(t.Context(), func(ref string) error {
		refs = append(refs, ref)
		return nil
	}))
	require.ElementsMatch(t, []string{"left", "open"}, refs)
	statuses, err := s.ListStatuses(t.Context())
	require.NoError(t, err)
	require.Len(t, statuses, 2)

	require.NoError(t, s.Abort(t.Context(), "left"))
	_, err = s.Status(t.Context(), "left")
	require.True(t, errdefs.IsNotFound(err), "%v", err)
	err = s.Abort(t.Context(), "left")
	require.True(t, errdefs.IsNotFound(err), "%v", err)
}

// Truncating to 0 starts the write over.
func TestTruncate(t *testing.T) {
	s := newStore(t)
	desc := descOf("hello")
	w, err := s.Writer(t.Context(), content.WithRef("a"), content.WithDescriptor(desc))
	require.NoError(t, err)
	_, err = w.Write([]byte("junk!"))
	require.NoError(t, err)
	require.NoError(t, w.Truncate(0))
	_, err = w.Write([]byte("hello"))
	require.NoError(t, err)
	require.NoError(t, w.Commit(t.Context(), desc.Size, desc.Digest))
	require.Equal(t, "hello", read(t, s, desc))
}

// A store written by containerd's local store reads the same: its blobs
// are where this store looks for them, and its writes are listed by ref.
func TestReadsContainerdLayout(t *testing.T) {
	root := t.TempDir()
	desc := descOf("hello")
	blob := filepath.Join(root, "blobs", "sha256", desc.Digest.Encoded())
	require.NoError(t, os.MkdirAll(filepath.Dir(blob), 0o755))
	require.NoError(t, os.WriteFile(blob, []byte("hello"), 0o644))
	s, err := New(root)
	require.NoError(t, err)
	ingest := s.ingestPath("old")
	require.NoError(t, os.MkdirAll(ingest, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(ingest, "ref"), []byte("old"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(ingest, "data"), []byte("he"), 0o644))

	require.Equal(t, "hello", read(t, s, desc))
	st, err := s.Status(t.Context(), "old")
	require.NoError(t, err)
	require.Equal(t, int64(2), st.Offset)
}

// Under containerd's metadata store, as the engine runs it: a blob written
// under a lease is recorded and readable, a second write of it is told it
// exists, and once the lease is gone garbage collection removes the blob
// and the write left behind.
func TestUnderMetadataStore(t *testing.T) {
	dir := t.TempDir()
	s, err := New(filepath.Join(dir, "content"))
	require.NoError(t, err)
	bdb, err := bolt.Open(filepath.Join(dir, "metadata.db"), 0o644, &bolt.Options{NoSync: true})
	require.NoError(t, err)
	t.Cleanup(func() { bdb.Close() })
	db := metadata.NewDB(bdb, s, map[string]ctdsnapshots.Snapshotter{})
	require.NoError(t, db.Init(t.Context()))
	ctx := namespaces.WithNamespace(context.Background(), "test")
	lm := metadata.NewLeaseManager(db)
	lease, err := lm.Create(ctx, leases.WithRandomID())
	require.NoError(t, err)
	leased := leases.WithLease(ctx, lease.ID)
	cs := db.ContentStore()

	desc := descOf("hello")
	require.NoError(t, content.WriteBlob(leased, cs, "import-1", strings.NewReader("hello"), desc))
	info, err := cs.Info(ctx, desc.Digest)
	require.NoError(t, err)
	require.Equal(t, int64(5), info.Size)
	got, err := content.ReadBlob(ctx, cs, desc)
	require.NoError(t, err)
	require.Equal(t, "hello", string(got))

	_, err = cs.Writer(leased, content.WithRef("import-2"), content.WithDescriptor(desc))
	require.True(t, errdefs.IsAlreadyExists(err), "%v", err)

	w, err := cs.Writer(leased, content.WithRef("left-behind"))
	require.NoError(t, err)
	_, err = w.Write([]byte("partial"))
	require.NoError(t, err)
	require.NoError(t, w.Close())

	require.NoError(t, lm.Delete(ctx, lease))
	_, err = db.GarbageCollect(ctx)
	require.NoError(t, err)
	_, err = s.Info(ctx, desc.Digest)
	require.True(t, errdefs.IsNotFound(err), "%v", err)
	var refs []string
	require.NoError(t, s.WalkStatusRefs(ctx, func(ref string) error {
		refs = append(refs, ref)
		return nil
	}))
	require.Empty(t, refs)
}

// unreadable is a blob's source that a copy may seek to its end, as the
// engine's section readers allow, but must never read a byte of.
type unreadable struct {
	t         *testing.T
	pos, size int64
}

func (u *unreadable) Read([]byte) (int, error) {
	if u.pos >= u.size {
		return 0, io.EOF
	}
	u.t.Error("the blob was read from its source")
	return 0, errdefs.ErrFailedPrecondition
}

func (u *unreadable) Seek(offset int64, whence int) (int64, error) {
	if whence != io.SeekStart {
		return 0, errdefs.ErrNotImplemented
	}
	u.pos = offset
	return offset, nil
}

// A store with the image's blobs holds them in place: containerd's metadata
// store records one without a byte read or written, it reads back from the
// image, and neither collection nor Delete removes it, since the store never
// wrote it.
func TestImageBlobsInPlace(t *testing.T) {
	dir := t.TempDir()
	image := filepath.Join(dir, "image")
	desc := descOf("hello")
	blob := filepath.Join(image, "blobs", "sha256", desc.Digest.Encoded())
	require.NoError(t, os.MkdirAll(filepath.Dir(blob), 0o755))
	require.NoError(t, os.WriteFile(blob, []byte("hello"), 0o444))
	s, err := New(filepath.Join(dir, "content"), WithImageBlobs(image))
	require.NoError(t, err)
	bdb, err := bolt.Open(filepath.Join(dir, "metadata.db"), 0o644, &bolt.Options{NoSync: true})
	require.NoError(t, err)
	t.Cleanup(func() { bdb.Close() })
	db := metadata.NewDB(bdb, s, map[string]ctdsnapshots.Snapshotter{})
	require.NoError(t, db.Init(t.Context()))
	ctx := namespaces.WithNamespace(context.Background(), "test")
	lm := metadata.NewLeaseManager(db)
	lease, err := lm.Create(ctx, leases.WithRandomID())
	require.NoError(t, err)
	leased := leases.WithLease(ctx, lease.ID)
	cs := db.ContentStore()

	require.NoError(t, content.WriteBlob(leased, cs, "import-1", &unreadable{t: t, size: desc.Size}, desc))
	info, err := cs.Info(ctx, desc.Digest)
	require.NoError(t, err)
	require.Equal(t, int64(5), info.Size)
	got, err := content.ReadBlob(ctx, cs, desc)
	require.NoError(t, err)
	require.Equal(t, "hello", string(got))
	_, err = os.Stat(filepath.Join(dir, "content", "blobs", "sha256", desc.Digest.Encoded()))
	require.ErrorIs(t, err, os.ErrNotExist, "the store wrote no copy")
	var walked []digest.Digest
	require.NoError(t, s.Walk(ctx, func(info content.Info) error {
		walked = append(walked, info.Digest)
		return nil
	}))
	require.Empty(t, walked, "Walk lists only what the store wrote")

	require.NoError(t, lm.Delete(ctx, lease))
	_, err = db.GarbageCollect(ctx)
	require.NoError(t, err)
	_, err = cs.Info(ctx, desc.Digest)
	require.True(t, errdefs.IsNotFound(err), "the record is collected: %v", err)
	require.True(t, errdefs.IsNotFound(s.Delete(ctx, desc.Digest)))
	data, err := os.ReadFile(blob)
	require.NoError(t, err)
	require.Equal(t, "hello", string(data), "the image's blob stays")
	require.Equal(t, "hello", read(t, s, desc))
}
