package snapshots

import (
	"archive/tar"
	"bufio"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/pkg/labels"
	cerrdefs "github.com/containerd/errdefs"
	"github.com/dagger/dagger/internal/buildkit/util/compression"
	"github.com/dagger/dagger/util/compressutil"
	digest "github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

// A layer the remote cache carries is a listing and its contents, where an
// image carries a tar. The listing is the layer's tar with the body of every
// regular file of at least ListingInlineMax bytes left out: such an entry
// keeps its header, with its size set to zero and PAX records naming the
// content blob that holds its bytes. Smaller files keep their bodies in the
// listing. A content blob is one file's bytes, compressed as a layer is, so
// the same file makes the same blob on every engine and the store keeps it
// once, however many layers hold it. A chain carries each of a layer's
// content blobs before the layer's listing, the first time the chain needs
// it, so every prefix of the chain carries what its layers need.
//
// Importing a listing rebuilds the layer's tar from it and its contents and
// applies that, so a layer comes back exactly as its tar would have.
const (
	MediaTypeListing     = "application/vnd.dagger.listing.v1.tar"
	MediaTypeListingZstd = MediaTypeListing + "+zstd"
	MediaTypeContent     = "application/vnd.dagger.content.v1"
	MediaTypeContentZstd = MediaTypeContent + "+zstd"

	// ListingInlineMax is the smallest file whose bytes go in a content blob
	// of their own. On a fill of today's main's workload, 273,642 of the
	// 407,409 distinct files the store took in were under 4 KB and held 0.4%
	// of their bytes: a blob each would cost a content commit and a lease
	// entry per file, and save almost nothing.
	ListingInlineMax = 64 << 10

	// The PAX records of an entry whose body is a content blob: the blob's
	// digest and size, and the file's size. Its tar entry's format and the
	// standard PAX records it had, which the listing's own PAX header may add
	// to, are recorded too: the rebuilt entry is the tar's, byte for byte.
	paxContentDigest = "DAGGER.content.digest"
	paxContentSize   = "DAGGER.content.size"
	paxContentRaw    = "DAGGER.content.raw"
	paxContentFormat = "DAGGER.content.format"
	paxContentKeys   = "DAGGER.content.keys"
	paxContentPrefix = "DAGGER.content."
)

// paxBasicKeys are the PAX records archive/tar writes from a header's own
// fields when they need one, as its writer names them.
var paxBasicKeys = map[string]bool{
	"path": true, "linkpath": true, "size": true, "uid": true, "gid": true,
	"uname": true, "gname": true, "mtime": true, "atime": true, "ctime": true,
}

// IsListing reports whether desc is a listing.
func IsListing(desc ocispecs.Descriptor) bool {
	return desc.MediaType == MediaTypeListing || desc.MediaType == MediaTypeListingZstd
}

// IsContent reports whether desc is a content blob.
func IsContent(desc ocispecs.Descriptor) bool {
	return desc.MediaType == MediaTypeContent || desc.MediaType == MediaTypeContentZstd
}

// listingMediaType returns the media types of a listing and its content
// blobs compressed as t, and false for a compression a listing doesn't come
// in: only zstd, whose writer makes the same blob of the same bytes on every
// engine, or none.
func listingMediaType(t compression.Type) (listing, content string, ok bool) {
	switch t {
	case compression.Uncompressed:
		return MediaTypeListing, MediaTypeContent, true
	case compression.Zstd:
		return MediaTypeListingZstd, MediaTypeContentZstd, true
	}
	return "", "", false
}

// listingCompressor turns compress into the compressor of a layer's
// listing. What a differ writes through it is the layer's tar; what reaches
// the differ's blob is the listing, compressed by compress, and the body of
// each file of at least ListingInlineMax bytes goes into a content blob of
// its own in cs, compressed by compress as well. The differ labels the
// listing with the digest of the tar it wrote, which is the layer's diff ID
// whether it travels as a tar or as a listing. contents returns the content
// blobs of the last listing written, in the order it first names them, each
// once, after its writer has closed.
func listingCompressor(ctx context.Context, cs content.Store, compress compression.Compressor, contentType, ref string) (_ compression.Compressor, contents func() []ocispecs.Descriptor) {
	var listed []ocispecs.Descriptor
	return func(dest io.Writer, mediaType string) (io.WriteCloser, error) {
		listing, err := compress(dest, mediaType)
		if err != nil {
			return nil, err
		}
		pr, pw := io.Pipe()
		w := &listingWriter{pw: pw, done: make(chan struct{})}
		go func() {
			defer close(w.done)
			split, err := splitLayerTar(ctx, cs, pr, listing, compress, contentType, ref)
			if err == nil {
				// The tar reader stops at the archive's end; what the
				// differ writes after it is read, so its writes end.
				_, err = io.Copy(io.Discard, pr)
			}
			if cerr := listing.Close(); err == nil {
				err = cerr
			}
			pr.CloseWithError(err)
			listed, w.err = split, err
		}()
		return w, nil
	}, func() []ocispecs.Descriptor { return listed }
}

// listingWriter is a listing compressor's writer: the layer's tar goes
// through it to the split, and Close waits for the split to end.
type listingWriter struct {
	pw   *io.PipeWriter
	done chan struct{}
	err  error
}

func (w *listingWriter) Write(p []byte) (int, error) { return w.pw.Write(p) }

func (w *listingWriter) Close() error {
	_ = w.pw.Close()
	<-w.done
	return w.err
}

const (
	// splitWorkers is how many of a layer's content blobs compress at once: as
	// many as the zstd writer compresses a large layer's frames at once. A
	// content blob of at most one frame, 8 MiB, compresses on one core, so
	// one after another a layer of many such files would take several times
	// its tar's time.
	splitWorkers = 4
	// splitHeld bounds the bytes of a layer's files read ahead of its
	// listing, while their content blobs compress or earlier ones finish: the
	// zstd writer's own frames in flight for a large layer, four of 8 MiB. A
	// file larger than one frame isn't held: it compresses as it is read,
	// its frames at once.
	splitHeld      = 32 << 20
	splitHeldLarge = splitHeld / splitWorkers
)

// splitEntry is one entry of a layer's tar on its way into the listing: its
// body when the listing keeps it, or its content blob, done once compressed.
type splitEntry struct {
	hdr  *tar.Header
	body []byte
	held int64
	// For a content blob: closed once desc or err is set.
	done chan struct{}
	desc ocispecs.Descriptor
	err  error
}

// splitLayerTar reads a layer's tar from r and writes its listing to listing
// and the content blobs of its larger files into cs, compressed by compress
// as contentType, up to splitWorkers at once. The listing is written in the
// tar's order, each content's entry once its blob is written. It returns the
// content blobs in the order the listing first names them, each once.
func splitLayerTar(ctx context.Context, cs content.Store, r io.Reader, listing io.Writer, compress compression.Compressor, contentType, ref string) ([]ocispecs.Descriptor, error) {
	eg, ctx := errgroup.WithContext(ctx)
	held := semaphore.NewWeighted(splitHeld)
	workers := make(chan struct{}, splitWorkers)
	entries := make(chan *splitEntry, 256)
	writeContent := func(e *splitEntry, body func(io.Writer) error) {
		e.desc, e.err = writeCompressedBlob(ctx, cs, ref+"-content-"+rand.Text(), contentType, compress, body)
		if e.err != nil {
			e.err = errors.Wrapf(e.err, "content of %s", e.hdr.Name)
		}
		close(e.done)
	}
	eg.Go(func() error {
		defer close(entries)
		tr := tar.NewReader(r)
		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return errors.Wrap(err, "read layer tar")
			}
			e := &splitEntry{hdr: hdr}
			isContent := hdr.Typeflag == tar.TypeReg && hdr.Size >= ListingInlineMax
			if isContent && hdr.Size > splitHeldLarge {
				e.done = make(chan struct{})
				select {
				case workers <- struct{}{}:
				case <-ctx.Done():
					return context.Cause(ctx)
				}
				writeContent(e, func(w io.Writer) error {
					_, err := io.CopyN(w, tr, hdr.Size)
					return err
				})
				<-workers
				if e.err != nil {
					return e.err
				}
			} else {
				e.held = max(hdr.Size, 1)
				if err := held.Acquire(ctx, e.held); err != nil {
					return err
				}
				e.body = make([]byte, hdr.Size)
				if _, err := io.ReadFull(tr, e.body); err != nil {
					held.Release(e.held)
					return errors.Wrapf(err, "read %s", hdr.Name)
				}
				if isContent {
					e.done = make(chan struct{})
					select {
					case workers <- struct{}{}:
					case <-ctx.Done():
						held.Release(e.held)
						return context.Cause(ctx)
					}
					body := e.body
					e.body = nil
					eg.Go(func() error {
						defer func() { <-workers }()
						writeContent(e, func(w io.Writer) error {
							_, err := w.Write(body)
							return err
						})
						held.Release(e.held)
						return e.err
					})
				}
			}
			select {
			case entries <- e:
			case <-ctx.Done():
				return context.Cause(ctx)
			}
		}
	})
	var contents []ocispecs.Descriptor
	eg.Go(func() error {
		seen := map[digest.Digest]bool{}
		tw := tar.NewWriter(listing)
		for e := range entries {
			if e.done == nil {
				err := tw.WriteHeader(e.hdr)
				if err == nil {
					_, err = tw.Write(e.body)
				}
				held.Release(e.held)
				if err != nil {
					return err
				}
				continue
			}
			select {
			case <-e.done:
			case <-ctx.Done():
				return context.Cause(ctx)
			}
			if e.err != nil {
				return e.err
			}
			if !seen[e.desc.Digest] {
				seen[e.desc.Digest] = true
				contents = append(contents, e.desc)
			}
			if err := tw.WriteHeader(contentHeader(e.hdr, e.desc)); err != nil {
				return err
			}
		}
		if err := context.Cause(ctx); err != nil {
			return err
		}
		return tw.Close()
	})
	if err := eg.Wait(); err != nil {
		return nil, err
	}
	return contents, nil
}

// contentHeader is the listing's entry for a file whose body is desc: the
// file's header, sized zero, with the PAX records that name desc and keep
// what the rebuilt entry needs to be the tar's.
func contentHeader(hdr *tar.Header, desc ocispecs.Descriptor) *tar.Header {
	entry := *hdr
	entry.Size = 0
	entry.PAXRecords = clonePAX(hdr.PAXRecords)
	entry.PAXRecords[paxContentDigest] = desc.Digest.String()
	entry.PAXRecords[paxContentSize] = strconv.FormatInt(desc.Size, 10)
	entry.PAXRecords[paxContentRaw] = strconv.FormatInt(hdr.Size, 10)
	entry.PAXRecords[paxContentFormat] = strconv.Itoa(int(hdr.Format))
	var basic []string
	for k := range hdr.PAXRecords {
		if paxBasicKeys[k] {
			basic = append(basic, k)
		}
	}
	if len(basic) > 0 {
		slices.Sort(basic)
		entry.PAXRecords[paxContentKeys] = strings.Join(basic, ",")
	}
	entry.Format = tar.FormatPAX
	return &entry
}

func clonePAX(in map[string]string) map[string]string {
	out := make(map[string]string, len(in)+3)
	for k, v := range in {
		out[k] = v
	}
	return out
}

// writeCompressedBlob writes what write produces, compressed by compress,
// into the content store as one blob of mediaType, and returns its
// descriptor, with the uncompressed digest as its annotation, as a layer's
// has. A blob the store already has is that blob.
func writeCompressedBlob(ctx context.Context, cs content.Store, ref, mediaType string, compress compression.Compressor, write func(io.Writer) error) (_ ocispecs.Descriptor, rerr error) {
	cw, err := content.OpenWriter(ctx, cs, content.WithRef(ref), content.WithDescriptor(ocispecs.Descriptor{MediaType: mediaType}))
	if err != nil {
		return ocispecs.Descriptor{}, errors.Wrap(err, "open content writer")
	}
	defer func() {
		if cw != nil {
			ctx := context.WithoutCancel(ctx)
			_ = cs.Abort(ctx, ref)
			_ = cw.Close()
		}
	}()
	if err := cw.Truncate(0); err != nil {
		return ocispecs.Descriptor{}, err
	}
	buf := bufio.NewWriterSize(cw, 128<<10)
	raw := digest.SHA256.Digester()
	var dest io.WriteCloser = nopWriteCloser{buf}
	if compress != nil {
		if dest, err = compress(buf, mediaType); err != nil {
			return ocispecs.Descriptor{}, errors.Wrap(err, "compress")
		}
	}
	if err := write(io.MultiWriter(dest, raw.Hash())); err != nil {
		_ = dest.Close()
		return ocispecs.Descriptor{}, err
	}
	if err := dest.Close(); err != nil {
		return ocispecs.Descriptor{}, err
	}
	if err := buf.Flush(); err != nil {
		return ocispecs.Descriptor{}, err
	}
	dgst := cw.Digest()
	lbls := map[string]string{labels.LabelUncompressed: raw.Digest().String()}
	if err := cw.Commit(ctx, 0, dgst, content.WithLabels(lbls)); err != nil && !cerrdefs.IsAlreadyExists(err) {
		return ocispecs.Descriptor{}, errors.Wrap(err, "commit")
	}
	_ = cw.Close()
	cw = nil
	info, err := cs.Info(ctx, dgst)
	if err != nil {
		return ocispecs.Descriptor{}, err
	}
	return ocispecs.Descriptor{
		MediaType:   mediaType,
		Digest:      dgst,
		Size:        info.Size,
		Annotations: map[string]string{labels.LabelUncompressed: raw.Digest().String()},
	}, nil
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

// listingContents returns the content blobs a listing names, in the order it
// first names them, each once.
func listingContents(ctx context.Context, cs content.Provider, listing ocispecs.Descriptor) ([]ocispecs.Descriptor, error) {
	rc, err := openListing(ctx, cs, listing)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	contentType := listingContentType(listing)
	var contents []ocispecs.Descriptor
	seen := map[digest.Digest]bool{}
	tr := tar.NewReader(rc)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return contents, nil
		}
		if err != nil {
			return nil, errors.Wrapf(err, "read listing %s", listing.Digest)
		}
		desc, ok, err := contentOf(hdr, contentType)
		if err != nil {
			return nil, errors.Wrapf(err, "listing %s", listing.Digest)
		}
		if ok && !seen[desc.Digest] {
			seen[desc.Digest] = true
			contents = append(contents, desc)
		}
	}
}

// contentOf returns the content blob an entry of a listing names, if any.
func contentOf(hdr *tar.Header, contentType string) (ocispecs.Descriptor, bool, error) {
	ds, ok := hdr.PAXRecords[paxContentDigest]
	if !ok {
		return ocispecs.Descriptor{}, false, nil
	}
	dgst, err := digest.Parse(ds)
	if err != nil {
		return ocispecs.Descriptor{}, false, errors.Wrapf(err, "entry %s names an invalid content", hdr.Name)
	}
	size, err := strconv.ParseInt(hdr.PAXRecords[paxContentSize], 10, 64)
	if err != nil {
		return ocispecs.Descriptor{}, false, errors.Wrapf(err, "entry %s has an invalid content size", hdr.Name)
	}
	return ocispecs.Descriptor{MediaType: contentType, Digest: dgst, Size: size}, true, nil
}

func openListing(ctx context.Context, cs content.Provider, listing ocispecs.Descriptor) (io.ReadCloser, error) {
	ra, err := cs.ReaderAt(ctx, listing)
	if err != nil {
		return nil, errors.Wrapf(err, "open listing %s", listing.Digest)
	}
	r := content.NewReader(ra)
	if listing.MediaType == MediaTypeListing {
		return struct {
			io.Reader
			io.Closer
		}{r, ra}, nil
	}
	ds, err := compressutil.DecompressStream(r)
	if err != nil {
		_ = ra.Close()
		return nil, errors.Wrapf(err, "decompress listing %s", listing.Digest)
	}
	return struct {
		io.Reader
		io.Closer
	}{ds, closers{ds, ra}}, nil
}

type closers []io.Closer

func (c closers) Close() error {
	var err error
	for _, x := range c {
		if e := x.Close(); e != nil && err == nil {
			err = e
		}
	}
	return err
}

// listingTar is the layer's tar a listing stands for, rebuilt on a goroutine
// of its own as it is read (writeExpandedListing).
type listingTar struct {
	r    *io.PipeReader
	done chan struct{}
	// read counts the listing's blob bytes read so far.
	read atomic.Int64
}

// errListingTarClosed stops a rebuild nothing reads any more.
var errListingTarClosed = errors.New("listing tar closed")

// openListingTar starts rebuilding the layer's tar of listing, which must be
// in cs with every content blob it names. The caller closes it.
func openListingTar(ctx context.Context, cs content.Provider, listing ocispecs.Descriptor) (*listingTar, error) {
	ra, err := cs.ReaderAt(ctx, listing)
	if err != nil {
		return nil, errors.Wrapf(err, "open listing %s", listing.Digest)
	}
	l := &listingTar{done: make(chan struct{})}
	var r io.Reader = &countingSectionReader{r: io.NewSectionReader(ra, 0, ra.Size()), n: &l.read}
	var ds io.ReadCloser
	if listing.MediaType == MediaTypeListingZstd {
		if ds, err = compressutil.DecompressStream(r); err != nil {
			_ = ra.Close()
			return nil, errors.Wrapf(err, "decompress listing %s", listing.Digest)
		}
		r = ds
	}
	pr, pw := io.Pipe()
	l.r = pr
	go func() {
		defer close(l.done)
		defer ra.Close()
		if ds != nil {
			defer ds.Close()
		}
		pw.CloseWithError(writeExpandedListing(ctx, pw, r, cs, listingContentType(listing)))
	}()
	return l, nil
}

func (l *listingTar) Read(p []byte) (int, error) { return l.r.Read(p) }

// Close stops the rebuild and waits for it to end.
func (l *listingTar) Close() {
	l.r.CloseWithError(errListingTarClosed)
	<-l.done
}

// listingContentType is the media type of the content blobs listing names.
func listingContentType(listing ocispecs.Descriptor) string {
	if listing.MediaType == MediaTypeListingZstd {
		return MediaTypeContentZstd
	}
	return MediaTypeContent
}

// writeExpandedListing writes the layer's tar a decompressed listing stands
// for: each entry whose body is a content blob gets its size back and its
// body from the blob, which must be in cs, and loses the PAX records that
// named it. Every other entry is the listing's.
func writeExpandedListing(ctx context.Context, w io.Writer, listing io.Reader, cs content.Provider, contentType string) error {
	tr := tar.NewReader(listing)
	tw := tar.NewWriter(w)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return errors.Wrap(err, "read listing")
		}
		desc, ok, err := contentOf(hdr, contentType)
		if err != nil {
			return err
		}
		if !ok {
			if err := tw.WriteHeader(hdr); err != nil {
				return err
			}
			if _, err := io.CopyN(tw, tr, hdr.Size); err != nil {
				return err
			}
			continue
		}
		raw, err := strconv.ParseInt(hdr.PAXRecords[paxContentRaw], 10, 64)
		if err != nil {
			return errors.Wrapf(err, "entry %s has an invalid size", hdr.Name)
		}
		format, err := strconv.Atoi(hdr.PAXRecords[paxContentFormat])
		if err != nil {
			return errors.Wrapf(err, "entry %s has an invalid format", hdr.Name)
		}
		kept := map[string]bool{}
		if keys := hdr.PAXRecords[paxContentKeys]; keys != "" {
			for _, k := range strings.Split(keys, ",") {
				kept[k] = true
			}
		}
		out := *hdr
		out.Size = raw
		out.Format = tar.Format(format)
		out.PAXRecords = clonePAX(hdr.PAXRecords)
		for k := range out.PAXRecords {
			if strings.HasPrefix(k, paxContentPrefix) || paxBasicKeys[k] && !kept[k] {
				delete(out.PAXRecords, k)
			}
		}
		if err := tw.WriteHeader(&out); err != nil {
			return err
		}
		if err := copyContent(ctx, tw, cs, desc, raw); err != nil {
			return errors.Wrapf(err, "content of %s", hdr.Name)
		}
	}
	return tw.Close()
}

// copyContent writes a content blob's file bytes to w, checking the file
// against its size. The content store checked the blob against its digest
// when it was written, as it does every blob.
func copyContent(ctx context.Context, w io.Writer, cs content.Provider, desc ocispecs.Descriptor, raw int64) error {
	ra, err := cs.ReaderAt(ctx, desc)
	if err != nil {
		return err
	}
	defer ra.Close()
	var r io.Reader = io.NewSectionReader(ra, 0, desc.Size)
	if desc.MediaType == MediaTypeContentZstd {
		ds, err := compressutil.DecompressStream(r)
		if err != nil {
			return err
		}
		defer ds.Close()
		r = ds
	}
	// One byte past the file's size shows a blob holding more than its file.
	n, err := io.Copy(w, io.LimitReader(r, raw+1))
	if err != nil {
		return err
	}
	if n != raw {
		return fmt.Errorf("content %s has %d bytes, its entry says %d", desc.Digest, n, raw)
	}
	return nil
}
