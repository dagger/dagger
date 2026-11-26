package snapshots

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/plugins/content/local"
	"github.com/dagger/dagger/internal/buildkit/util/compression"
	digest "github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

// fileBytes is n bytes of text that compresses, different for each seed.
func fileBytes(seed string, n int) string {
	var b strings.Builder
	for i := 0; b.Len() < n; i++ {
		fmt.Fprintf(&b, "%s line %d of a file that is long enough to keep\n", seed, i*7919%104729)
	}
	return b.String()[:n]
}

// splitTestTar is a layer's tar as the engine's differs write one, with
// every kind of entry a listing has to carry: directories, files on both
// sides of ListingInlineMax and two with the same bytes, a file too large to
// be held while it compresses, a name too long for a ustar header, PAX
// records the tar keeps, a time with nanoseconds, a symlink, a hard link to
// a file whose body left the listing, and a whiteout.
func splitTestTar(t *testing.T) []byte {
	t.Helper()
	mtime := time.Unix(1700000000, 0).UTC()
	shared := fileBytes("shared", ListingInlineMax+36<<10)
	long := strings.Repeat("deep/", 30) + "big.bin"
	files := map[string]string{
		"dir/small.txt":    "small bytes",
		"dir/shared-a.bin": shared,
		"dir/shared-b.bin": shared,
		"dir/at-max.bin":   fileBytes("at max", ListingInlineMax),
		"dir/under.bin":    fileBytes("under max", ListingInlineMax-1),
		"dir/empty":        "",
		long:               fileBytes("long name", ListingInlineMax+6<<10),
		"xattr.bin":        fileBytes("xattr", ListingInlineMax+16<<10),
		"nanos.bin":        fileBytes("nanos", ListingInlineMax+26<<10),
		"large.bin":        fileBytes("large", splitHeldLarge+1),
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	write := func(hdr *tar.Header, body string) {
		require.NoError(t, tw.WriteHeader(hdr))
		_, err := io.WriteString(tw, body)
		require.NoError(t, err)
	}
	dir := func(name string) {
		write(&tar.Header{Name: name, Typeflag: tar.TypeDir, Mode: 0o755, ModTime: mtime, Format: tar.FormatPAX}, "")
	}
	file := func(name string, hdr tar.Header) {
		hdr.Name, hdr.Typeflag, hdr.Size = name, tar.TypeReg, int64(len(files[name]))
		if hdr.Mode == 0 {
			hdr.Mode = 0o644
		}
		if hdr.ModTime.IsZero() {
			hdr.ModTime = mtime
		}
		write(&hdr, files[name])
	}
	dir("dir/")
	file("dir/small.txt", tar.Header{Format: tar.FormatPAX})
	file("dir/shared-a.bin", tar.Header{Format: tar.FormatPAX, Uid: 1000, Gid: 1000, Uname: "auser", Gname: "agroup"})
	file("dir/shared-b.bin", tar.Header{Format: tar.FormatPAX, Mode: 0o600})
	file("dir/at-max.bin", tar.Header{})
	file("dir/under.bin", tar.Header{})
	file("dir/empty", tar.Header{Format: tar.FormatPAX})
	file(long, tar.Header{Format: tar.FormatPAX})
	file("xattr.bin", tar.Header{Format: tar.FormatPAX, PAXRecords: map[string]string{"SCHILY.xattr.user.note": "kept"}})
	file("nanos.bin", tar.Header{Format: tar.FormatPAX, ModTime: mtime.Add(123456789)})
	file("large.bin", tar.Header{Format: tar.FormatPAX})
	write(&tar.Header{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "dir/small.txt", ModTime: mtime, Format: tar.FormatPAX}, "")
	write(&tar.Header{Name: "hard.bin", Typeflag: tar.TypeLink, Linkname: "dir/shared-a.bin", ModTime: mtime, Format: tar.FormatPAX}, "")
	write(&tar.Header{Name: ".wh.gone", Typeflag: tar.TypeChar, ModTime: mtime, Format: tar.FormatPAX}, "")
	require.NoError(t, tw.Close())
	return buf.Bytes()
}

// A layer's tar splits into a listing and a content blob for each distinct
// body of ListingInlineMax bytes or more, and the listing with its contents
// rebuilds the tar byte for byte: the diff ID the layer is recorded under is
// the rebuilt tar's.
func TestListingSplitRebuildsTheTar(t *testing.T) {
	t.Parallel()
	layer := splitTestTar(t)
	for _, comp := range []compression.Type{compression.Uncompressed, compression.Zstd} {
		t.Run(comp.String(), func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			cs, err := local.NewStore(t.TempDir())
			require.NoError(t, err)
			listingType, contentType, ok := listingMediaType(comp)
			require.True(t, ok)
			compress, _ := comp.Compress(ctx, compression.New(comp))
			listingCompress, contents := listingCompressor(ctx, cs, compress, contentType, "split-test")

			var listingBlob bytes.Buffer
			w, err := listingCompress(&listingBlob, listingType)
			require.NoError(t, err)
			// A differ may write past the archive's end.
			_, err = w.Write(append(append([]byte(nil), layer...), make([]byte, 4096)...))
			require.NoError(t, err)
			require.NoError(t, w.Close())

			// shared-a and shared-b are one content; at-max, the long name,
			// xattr, nanos and large are one each; under is in the listing.
			listed := contents()
			require.Len(t, listed, 6)
			for _, c := range listed {
				require.Equal(t, contentType, c.MediaType)
				info, err := cs.Info(ctx, c.Digest)
				require.NoError(t, err)
				require.Equal(t, c.Size, info.Size)
			}

			listing := ocispecs.Descriptor{MediaType: listingType, Digest: digest.FromBytes(listingBlob.Bytes()), Size: int64(listingBlob.Len())}
			require.NoError(t, content.WriteBlob(ctx, cs, "listing", bytes.NewReader(listingBlob.Bytes()), listing))
			named, err := listingContents(ctx, cs, listing)
			require.NoError(t, err)
			require.Equal(t, digests(listed), digests(named), "the listing names the contents the split wrote, in its order")

			rebuilt, err := openListingTar(ctx, cs, listing)
			require.NoError(t, err)
			got, err := io.ReadAll(rebuilt)
			rebuilt.Close()
			require.NoError(t, err)
			require.Equal(t, digest.FromBytes(layer), digest.FromBytes(got), "the rebuilt tar is the layer's tar")
			require.Less(t, listingBlob.Len(), 2*ListingInlineMax, "no body of ListingInlineMax bytes or more is in the listing")
		})
	}
}

// A listing whose content is missing from the store fails to rebuild: a
// layer is never applied without its bytes.
func TestListingRebuildFailsWithoutItsContent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	layer := splitTestTar(t)
	cs, err := local.NewStore(t.TempDir())
	require.NoError(t, err)
	compress, _ := compression.Uncompressed.Compress(ctx, compression.New(compression.Uncompressed))
	listingCompress, contents := listingCompressor(ctx, cs, compress, MediaTypeContent, "missing-test")
	var listingBlob bytes.Buffer
	w, err := listingCompress(&listingBlob, MediaTypeListing)
	require.NoError(t, err)
	_, err = w.Write(layer)
	require.NoError(t, err)
	require.NoError(t, w.Close())
	require.NoError(t, cs.Delete(ctx, contents()[0].Digest))

	listing := ocispecs.Descriptor{MediaType: MediaTypeListing, Digest: digest.FromBytes(listingBlob.Bytes()), Size: int64(listingBlob.Len())}
	require.NoError(t, content.WriteBlob(ctx, cs, "listing", bytes.NewReader(listingBlob.Bytes()), listing))
	rebuilt, err := openListingTar(ctx, cs, listing)
	require.NoError(t, err)
	_, err = io.ReadAll(rebuilt)
	rebuilt.Close()
	require.Error(t, err)
}

// waitingWriterStore is a content store whose writers open only once
// release is closed.
type waitingWriterStore struct {
	content.Store
	release chan struct{}
}

func (s *waitingWriterStore) Writer(ctx context.Context, opts ...content.WriterOpt) (content.Writer, error) {
	select {
	case <-s.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return s.Store.Writer(ctx, opts...)
}

// A differ writes a layer's tar ahead of its split: while the split waits for
// the content store to write the layer's content blobs, the differ's writes
// of the rest of the tar don't wait, and the listing that ends up written
// rebuilds the tar as it would have.
func TestListingSplitDoesNotHoldTheDiffer(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	layer := splitTestTar(t)
	store, err := local.NewStore(t.TempDir())
	require.NoError(t, err)
	cs := &waitingWriterStore{Store: store, release: make(chan struct{})}
	listingType, contentType, ok := listingMediaType(compression.Zstd)
	require.True(t, ok)
	compress, _ := compression.Zstd.Compress(ctx, compression.New(compression.Zstd))
	listingCompress, contents := listingCompressor(ctx, cs, compress, contentType, "ahead-test")
	var listingBlob bytes.Buffer
	w, err := listingCompress(&listingBlob, listingType)
	require.NoError(t, err)

	wrote := make(chan error, 1)
	go func() {
		_, err := w.Write(layer)
		wrote <- err
	}()
	select {
	case err := <-wrote:
		close(cs.release)
		require.NoError(t, err)
	case <-time.After(30 * time.Second):
		select {
		case err := <-wrote:
			// It returned as the timer fired.
			close(cs.release)
			require.NoError(t, err)
		default:
			close(cs.release)
			require.NoError(t, <-wrote)
			require.NoError(t, w.Close())
			t.Fatal("the differ's write of the layer's tar waited for the split's content blobs")
		}
	}
	require.NoError(t, w.Close())

	listed := contents()
	require.Len(t, listed, 6)
	listing := ocispecs.Descriptor{MediaType: listingType, Digest: digest.FromBytes(listingBlob.Bytes()), Size: int64(listingBlob.Len())}
	require.NoError(t, content.WriteBlob(ctx, store, "listing", bytes.NewReader(listingBlob.Bytes()), listing))
	rebuilt, err := openListingTar(ctx, store, listing)
	require.NoError(t, err)
	got, err := io.ReadAll(rebuilt)
	rebuilt.Close()
	require.NoError(t, err)
	require.Equal(t, digest.FromBytes(layer), digest.FromBytes(got), "the rebuilt tar is the layer's tar")
}

// A split buffer holds what its writer writes in at most its size of pieces
// and hands it to its reader in order: a write returns once its bytes are
// held, and one that needs a piece past the size waits for a read to give
// one back, even with less than the size unread. The reader gets io.EOF once
// the writer has closed and everything is read, and a reader's error ends a
// waiting write with that error.
func TestSplitBuffer(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		data := []byte(fileBytes("buffer", 5<<20+12345))
		b := newSplitBuffer(3 << 20)
		n, err := b.Write(data[:3<<20])
		require.NoError(t, err)
		require.Equal(t, 3<<20, n)
		// A byte read leaves its piece held, so the next byte has no room.
		got := make([]byte, 1, len(data))
		_, err = io.ReadFull(b, got)
		require.NoError(t, err)

		held := make(chan struct{})
		wrote := make(chan error, 1)
		go func() {
			_, err := b.Write(data[3<<20 : 3<<20+1])
			close(held)
			if err == nil {
				_, err = b.Write(data[3<<20+1:])
			}
			if err == nil {
				b.Close()
			}
			wrote <- err
		}()
		synctest.Wait()
		select {
		case <-held:
			t.Error("a byte past the buffer's pieces was held before a read gave one back")
		default:
		}
		rest, err := io.ReadAll(b)
		require.NoError(t, err)
		require.NoError(t, <-wrote)
		require.Equal(t, data, append(got, rest...))
		n, err = b.Read(make([]byte, 1))
		require.Equal(t, 0, n)
		require.ErrorIs(t, err, io.EOF)

		failed := errors.New("the split failed")
		b = newSplitBuffer(1 << 20)
		_, err = b.Write(data[:1<<20])
		require.NoError(t, err)
		go func() {
			_, err := b.Write(data[1<<20 : 2<<20])
			wrote <- err
		}()
		synctest.Wait()
		b.CloseWithError(failed)
		require.ErrorIs(t, <-wrote, failed)
		_, err = b.Write(data[:1])
		require.ErrorIs(t, err, failed)
	})
}

func digests(descs []ocispecs.Descriptor) []digest.Digest {
	out := make([]digest.Digest, len(descs))
	for i, d := range descs {
		out[i] = d.Digest
	}
	return out
}
