package snapshots

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
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

func digests(descs []ocispecs.Descriptor) []digest.Digest {
	out := make([]digest.Digest, len(descs))
	for i, d := range descs {
		out[i] = d.Digest
	}
	return out
}
