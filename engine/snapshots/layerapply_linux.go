package snapshots

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/diff"
	"github.com/containerd/containerd/v2/core/diff/apply"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/pkg/archive"
	"github.com/containerd/continuity/fs"
	"github.com/containerd/continuity/sysx"
	"github.com/containerd/log"
	"github.com/containerd/typeurl/v2"
	"github.com/dagger/dagger/util/compressutil"
	"github.com/moby/sys/userns"
	digest "github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sys/unix"
)

// layerApplyWorkers bounds the files of one layer written at once.
const layerApplyWorkers = 8

// layerApplyBufferedMax is the largest file a worker writes: its bytes are
// read from the tar into memory first. The goroutine reading the tar writes
// larger files itself.
const layerApplyBufferedMax = 1 << 20

// NewLayerApplier returns the engine's applier for image layers. It extracts
// a layer into a snapshot's directory as containerd's file system applier
// does, entry for entry, except that regular files are written by
// layerApplyWorkers goroutines while the tar is read. Mounts it doesn't
// write into directly (several mounts, a read-only bind, a user namespace)
// go to containerd's applier.
func NewLayerApplier(store content.Provider) diff.Applier {
	return &layerApplier{store: store, fallback: apply.NewFileSystemApplier(store)}
}

type layerApplier struct {
	store    content.Provider
	fallback diff.Applier
}

// layerTarget is the directory a layer is extracted into: an overlay's upper
// directory with its lower directories, or a parentless snapshot's directory.
type layerTarget struct {
	root    string
	parents []string
	overlay bool
}

// layerApplyTarget follows containerd's core/diff/apply: an overlay mount is
// written through its upper directory, with overlay whiteouts, and a bind
// mount is written through its source.
func layerApplyTarget(mounts []mount.Mount) (layerTarget, bool) {
	// Overlay whiteouts (mknod c 0 0) don't work in a user namespace.
	if len(mounts) != 1 || userns.RunningInUserNS() {
		return layerTarget{}, false
	}
	m := mounts[0]
	switch m.Type {
	case "overlay":
		var t layerTarget
		for _, o := range m.Options {
			if upper, ok := strings.CutPrefix(o, "upperdir="); ok {
				t.root = upper
			} else if lower, ok := strings.CutPrefix(o, "lowerdir="); ok {
				t.parents = strings.Split(lower, ":")
			}
		}
		t.overlay = true
		return t, t.root != ""
	case "bind":
		for _, o := range m.Options {
			if o == "ro" {
				return layerTarget{}, false
			}
		}
		return layerTarget{root: m.Source}, m.Source != ""
	}
	return layerTarget{}, false
}

// layerTarStream is the layer's tar, read from blob. A layer media type
// containerd knows is read as its compressed-layer processor reads it, the
// processor containerd picks for it when nothing else is registered, as in
// the engine, except that a zstd decoder is reused across layers
// (compressutil.DecompressStream). Any other media type goes through
// containerd's processors, which are returned to be checked for errors once
// the tar has been read.
func layerTarStream(ctx context.Context, desc ocispecs.Descriptor, blob io.Reader, payloads map[string]typeurl.Any) (io.ReadCloser, []diff.StreamProcessor, error) {
	if compressed, err := images.DiffCompression(ctx, desc.MediaType); err == nil {
		if compressed == "" {
			return io.NopCloser(blob), nil, nil
		}
		ds, err := compressutil.DecompressStream(blob)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to decompress %s: %w", desc.MediaType, err)
		}
		return ds, nil, nil
	}
	processor := diff.NewProcessorChain(desc.MediaType, blob)
	processors := []diff.StreamProcessor{processor}
	for {
		var err error
		if processor, err = diff.GetProcessor(ctx, processor, payloads); err != nil {
			return nil, nil, fmt.Errorf("failed to get stream processor for %s: %w", desc.MediaType, err)
		}
		processors = append(processors, processor)
		if processor.MediaType() == ocispecs.MediaTypeImageLayer {
			return processor, processors, nil
		}
	}
}

// Apply is containerd's fsApplier.Apply with the extraction replaced.
func (a *layerApplier) Apply(ctx context.Context, desc ocispecs.Descriptor, mounts []mount.Mount, opts ...diff.ApplyOpt) (ocispecs.Descriptor, error) {
	target, ok := layerApplyTarget(mounts)
	if !ok {
		return a.fallback.Apply(ctx, desc, mounts, opts...)
	}
	var config diff.ApplyConfig
	for _, o := range opts {
		if err := o(ctx, desc, &config); err != nil {
			return ocispecs.Descriptor{}, fmt.Errorf("failed to apply config opt: %w", err)
		}
	}

	ra, err := a.store.ReaderAt(ctx, desc)
	if err != nil {
		return ocispecs.Descriptor{}, fmt.Errorf("failed to get reader from content store: %w", err)
	}
	defer ra.Close()
	read := &applyCounter{r: content.NewReader(ra), progress: config.Progress}
	defer read.report()

	layer, processors, err := layerTarStream(ctx, desc, read, config.ProcessorPayloads)
	if err != nil {
		return ocispecs.Descriptor{}, err
	}
	defer layer.Close()

	trusted := trustedLayer(ctx)
	rc := startLayerReadAhead(layer, !trusted)
	defer rc.stop()
	if err := applyLayerTar(ctx, target, rc); err != nil {
		return ocispecs.Descriptor{}, err
	}
	if config.SyncFs {
		if err := syncFS(target.root); err != nil {
			return ocispecs.Descriptor{}, err
		}
	}
	// Read any trailing data
	if _, err := io.Copy(io.Discard, rc); err != nil {
		return ocispecs.Descriptor{}, err
	}
	for _, p := range processors {
		if ep, ok := p.(interface{ Err() error }); ok {
			if err := ep.Err(); err != nil {
				return ocispecs.Descriptor{}, err
			}
		}
	}
	applied := ocispecs.Descriptor{MediaType: ocispecs.MediaTypeImageLayer, Size: rc.n}
	if !trusted {
		applied.Digest = rc.digester.Digest()
	}
	return applied, nil
}

// layerReadAheadChunk and layerReadAheadChunks size the buffer between a
// layer's decompression and its extraction.
const (
	layerReadAheadChunk  = 1 << 20
	layerReadAheadChunks = 16
)

// layerReadAheadBufs holds read-ahead chunks between layers. An engine
// applies thousands of layers, most far smaller than a chunk (the parts its
// image ships average 300 KB), and a chunk made per layer was a megabyte
// allocated and zeroed for each.
var layerReadAheadBufs = sync.Pool{New: func() any {
	buf := make([]byte, layerReadAheadChunk)
	return &buf
}}

func getReadAheadChunk() []byte { return *layerReadAheadBufs.Get().(*[]byte) }

func putReadAheadChunk(buf []byte) {
	if cap(buf) == layerReadAheadChunk {
		buf = buf[:layerReadAheadChunk]
		layerReadAheadBufs.Put(&buf)
	}
}

// layerReadAhead reads a layer's uncompressed stream on its own goroutine,
// hashing and counting it there, up to layerReadAheadChunks chunks ahead of
// the extraction reading it. Without it, decompression, which runs in its
// own process (igzip or unpigz) behind a pipe, waits whenever the extraction
// does, and the two take turns instead of running together.
type layerReadAhead struct {
	chunks chan layerReadAheadResult
	free   chan []byte
	done   chan struct{}
	exited chan struct{}

	cur  []byte
	buf  []byte
	err  error
	once sync.Once

	// hash says whether to hash the stream: an apply of a layer the engine
	// trusts (trustedLayer) doesn't.
	hash bool
	// Set by the reading goroutine; read once it has exited.
	digester digest.Digester
	n        int64
}

type layerReadAheadResult struct {
	buf []byte
	n   int
	err error
}

func startLayerReadAhead(src io.Reader, hash bool) *layerReadAhead {
	ra := &layerReadAhead{
		hash:     hash,
		chunks:   make(chan layerReadAheadResult, layerReadAheadChunks),
		free:     make(chan []byte, layerReadAheadChunks+1),
		done:     make(chan struct{}),
		exited:   make(chan struct{}),
		digester: digest.Canonical.Digester(),
	}
	go ra.fill(src)
	return ra
}

func (ra *layerReadAhead) fill(src io.Reader) {
	defer close(ra.exited)
	hash := ra.digester.Hash()
	for {
		select {
		case <-ra.done:
			return
		default:
		}
		var buf []byte
		select {
		case buf = <-ra.free:
		default:
			buf = getReadAheadChunk()
		}
		n, err := 0, error(nil)
		for n < len(buf) && err == nil {
			var m int
			m, err = src.Read(buf[n:])
			n += m
		}
		if ra.hash {
			hash.Write(buf[:n])
		}
		ra.n += int64(n)
		select {
		case ra.chunks <- layerReadAheadResult{buf: buf, n: n, err: err}:
		case <-ra.done:
			putReadAheadChunk(buf)
			return
		}
		if err != nil {
			return
		}
	}
}

func (ra *layerReadAhead) Read(p []byte) (int, error) {
	for len(ra.cur) == 0 {
		if ra.err != nil {
			return 0, ra.err
		}
		if ra.buf != nil {
			select {
			case ra.free <- ra.buf:
			default:
			}
			ra.buf = nil
		}
		next := <-ra.chunks
		ra.buf, ra.cur, ra.err = next.buf, next.buf[:next.n], next.err
	}
	n := copy(p, ra.cur)
	ra.cur = ra.cur[n:]
	return n, nil
}

// stop ends the reading goroutine and waits for it, so nothing reads the
// stream once Apply has returned and closed it. Its chunks go back to the
// pool: nothing reads ra once it has stopped.
func (ra *layerReadAhead) stop() {
	ra.once.Do(func() { close(ra.done) })
	<-ra.exited
	if ra.buf != nil {
		putReadAheadChunk(ra.buf)
		ra.buf, ra.cur = nil, nil
	}
	for {
		select {
		case next := <-ra.chunks:
			putReadAheadChunk(next.buf)
		case buf := <-ra.free:
			putReadAheadChunk(buf)
		default:
			return
		}
	}
}

// applyCounter counts the bytes read through it and, like containerd's
// progress reader, reports the count before each read and at the end.
type applyCounter struct {
	r        io.Reader
	n        int64
	progress func(int64)
}

func (c *applyCounter) Read(p []byte) (int, error) {
	if c.progress != nil {
		c.progress(c.n)
	}
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

func (c *applyCounter) report() {
	if c.progress != nil {
		c.progress(c.n)
	}
}

func syncFS(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("failed to open %s: %w", path, err)
	}
	defer f.Close()
	if err := unix.Syncfs(int(f.Fd())); err != nil {
		return fmt.Errorf("failed to syncfs for %s: %w", path, err)
	}
	return nil
}

// layerExtraction is one layer's extraction: containerd's
// pkg/archive.applyNaive, with regular files handed to workers.
//
// Order: the goroutine reading the tar does every step but writing a regular
// file of at most layerApplyBufferedMax bytes, in the tar's order. A step
// that would see or change a path a worker may still be writing waits for
// the workers first: a lookup of that path or of a path under it, a
// whiteout, a removal and a hard link. Directory times are set after every
// file is written, as containerd sets them after every entry.
//
// Lookups the reader repeats are answered from what it already did. A
// directory's resolved path stays the same until a symlink is created or
// something is removed (fs.RootPath follows only symlinks), and a directory
// it knows to exist stays one until something is removed.
type layerExtraction struct {
	ctx     context.Context
	root    string
	parents []string
	overlay bool

	resolved map[string]string   // fs.RootPath's answer, by tar directory
	dirs     map[string]struct{} // directories known to exist
	unpacked map[string]struct{} // paths written by this layer
	queued   map[string]struct{} // paths handed to workers since they last finished

	writers *layerFileWriters
}

func applyLayerTar(ctx context.Context, t layerTarget, r io.Reader) (rerr error) {
	x := &layerExtraction{
		ctx:      ctx,
		root:     filepath.Clean(t.root),
		parents:  t.parents,
		overlay:  t.overlay,
		resolved: map[string]string{},
		dirs:     map[string]struct{}{},
		unpacked: map[string]struct{}{},
		queued:   map[string]struct{}{},
		writers:  startLayerFileWriters(ctx),
	}
	defer func() {
		if err := x.writers.stop(); rerr == nil {
			rerr = err
		}
	}()

	var dirs []*tar.Header
	tr := tar.NewReader(r)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if err := x.writers.failed(); err != nil {
			return err
		}

		hdr, err := tr.Next()
		if err == io.EOF {
			// end of tar archive
			break
		}
		if err != nil {
			return err
		}

		// Normalize name, for safety and for a simple is-root check
		hdr.Name = filepath.Clean(hdr.Name)

		// Split name and resolve symlinks for root directory.
		ppath, base := filepath.Split(hdr.Name)
		ppath, err = x.rootPath(ppath)
		if err != nil {
			return fmt.Errorf("failed to get root path: %w", err)
		}

		// Join to root before joining to parent path to ensure relative links are
		// already resolved based on the root before adding to parent.
		path := filepath.Join(ppath, filepath.Join("/", base))
		if path == x.root {
			log.G(ctx).Debugf("file %q ignored: resolved to root", hdr.Name)
			continue
		}

		// If file is not directly under root, ensure parent directory
		// exists or is created.
		if ppath != x.root {
			parentPath := ppath
			if base == "" {
				parentPath = filepath.Dir(path)
			}
			if err := x.mkparent(parentPath); err != nil {
				return err
			}
		}

		if err := validateWhiteout(path); err != nil {
			return err
		}
		writeFile, err := x.convertWhiteout(hdr, path)
		if err != nil {
			return fmt.Errorf("failed to convert whiteout file %q: %w", hdr.Name, err)
		}
		if !writeFile {
			continue
		}
		// If path exits we almost always just want to remove and replace it.
		// The only exception is when it is a directory *and* the file from
		// the layer is also a directory. Then we want to merge them (i.e.
		// just apply the metadata from the layer).
		if err := x.settle(path); err != nil {
			return err
		}
		if fi, err := os.Lstat(path); err == nil {
			if !fi.IsDir() || hdr.Typeflag != tar.TypeDir {
				if err := x.removing(); err != nil {
					return err
				}
				if err := os.RemoveAll(path); err != nil {
					return err
				}
			}
		}

		if err := x.create(path, hdr, tr); err != nil {
			return err
		}

		// Directory mtimes must be handled at the end to avoid further
		// file creation in them to modify the directory mtime
		if hdr.Typeflag == tar.TypeDir {
			dirs = append(dirs, hdr)
		}
		x.unpacked[path] = struct{}{}
	}

	if err := x.writers.finish(); err != nil {
		return err
	}
	for _, hdr := range dirs {
		path, err := x.rootPath(hdr.Name)
		if err != nil {
			return err
		}
		if err := chtimes(path, boundTime(latestTime(hdr.AccessTime, hdr.ModTime)), boundTime(hdr.ModTime)); err != nil {
			return err
		}
	}
	return nil
}

// rootPath is fs.RootPath(root, p), answered again from the first answer
// while no symlink has been created and nothing removed. "a/b/" and "a/b"
// resolve the same.
func (x *layerExtraction) rootPath(p string) (string, error) {
	p = strings.TrimSuffix(p, "/")
	if resolved, ok := x.resolved[p]; ok {
		return resolved, nil
	}
	resolved, err := fs.RootPath(x.root, p)
	if err != nil {
		return "", err
	}
	x.resolved[p] = resolved
	return resolved, nil
}

// changed forgets every remembered lookup: a symlink was created, or
// something removed.
func (x *layerExtraction) changed() {
	clear(x.resolved)
	clear(x.dirs)
}

// settle waits for the workers when one may still be writing path or a path
// it is under.
func (x *layerExtraction) settle(path string) error {
	if len(x.queued) == 0 {
		return nil
	}
	for p := path; len(p) > len(x.root); p = filepath.Dir(p) {
		if _, ok := x.queued[p]; ok {
			return x.finish()
		}
	}
	return nil
}

// finish waits until the workers have written every file handed to them.
func (x *layerExtraction) finish() error {
	clear(x.queued)
	return x.writers.finish()
}

// removing is called before anything is removed: it waits for the workers,
// and forgets every remembered lookup.
func (x *layerExtraction) removing() error {
	x.changed()
	return x.finish()
}

func (x *layerExtraction) convertWhiteout(hdr *tar.Header, path string) (bool, error) {
	base := filepath.Base(path)
	if !strings.HasPrefix(base, whiteoutPrefix) {
		return true, nil
	}
	if err := x.removing(); err != nil {
		return false, err
	}
	if x.overlay {
		return archive.OverlayConvertWhiteout(hdr, path)
	}

	// containerd's applyNaive's own whiteout handling: remove the target
	// files.
	dir := filepath.Dir(path)
	if base == whiteoutOpaqueDir {
		_, err := os.Lstat(dir)
		if err != nil {
			return false, err
		}
		err = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				if os.IsNotExist(err) {
					err = nil // parent was deleted
				}
				return err
			}
			if path == dir {
				return nil
			}
			if _, exists := x.unpacked[path]; !exists {
				err := os.RemoveAll(path)
				return err
			}
			return nil
		})
		return false, err
	}

	originalBase := base[len(whiteoutPrefix):]
	originalPath := filepath.Join(dir, originalBase)

	return false, os.RemoveAll(originalPath)
}

// mkparent is containerd's mkparent, skipping a directory known to exist.
func (x *layerExtraction) mkparent(path string) error {
	if _, ok := x.dirs[path]; ok {
		return nil
	}
	if err := x.settle(path); err != nil {
		return err
	}
	if err := x.makeParent(path); err != nil {
		return err
	}
	x.dirs[path] = struct{}{}
	return nil
}

func (x *layerExtraction) makeParent(path string) error {
	if dir, err := os.Lstat(path); err == nil {
		if dir.IsDir() {
			return nil
		}
		return &os.PathError{
			Op:   "mkparent",
			Path: path,
			Err:  syscall.ENOTDIR,
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	i := len(path)
	for i > len(x.root) && !os.IsPathSeparator(path[i-1]) {
		i--
	}

	if i > len(x.root)+1 {
		if err := x.mkparent(path[:i-1]); err != nil {
			return err
		}
	}

	if err := mkdir(path, 0755); err != nil {
		// Check that still doesn't exist
		dir, err1 := os.Lstat(path)
		if err1 == nil && dir.IsDir() {
			return nil
		}
		return err
	}

	for _, p := range x.parents {
		ppath, err := fs.RootPath(p, path[len(x.root):])
		if err != nil {
			return err
		}

		dir, err := os.Lstat(ppath)
		if err == nil {
			if !dir.IsDir() {
				// Replaced, do not copy attributes
				break
			}
			if err := copyDirInfo(dir, path); err != nil {
				return err
			}
			return copyUpXAttrs(path, ppath)
		} else if !os.IsNotExist(err) {
			return err
		}
	}

	log.G(x.ctx).Debugf("parent directory %q not found: default permissions(0755) used", path)

	return nil
}

// create is containerd's createTarFile, with a regular file of at most
// layerApplyBufferedMax bytes read into memory and handed to a worker.
func (x *layerExtraction) create(path string, hdr *tar.Header, reader io.Reader) error {
	// hdr.Mode is in linux format, which we can use for syscalls,
	// but for os.Foo() calls we need the mode converted to os.FileMode,
	// so use hdrInfo.Mode() (they differ for e.g. setuid bits)
	hdrInfo := hdr.FileInfo()

	switch hdr.Typeflag {
	case tar.TypeDir:
		// Create directory unless it exists as a directory already.
		// In that case we just want to merge the two
		if fi, err := os.Lstat(path); err != nil || !fi.IsDir() {
			if err := mkdir(path, hdrInfo.Mode()); err != nil {
				return err
			}
		} else if err := x.finish(); err != nil {
			// Files under it may still be queued, and a file takes some of
			// its directory's metadata when it is created, such as a
			// default ACL: they are created first, as in tar order.
			return err
		}

	//nolint:staticcheck // TypeRegA is deprecated but we may still receive an external tar with TypeRegA
	case tar.TypeReg, tar.TypeRegA:
		if hdr.Size <= layerApplyBufferedMax {
			data := make([]byte, hdr.Size)
			if _, err := io.ReadFull(reader, data); err != nil {
				return err
			}
			x.queued[path] = struct{}{}
			return x.writers.write(layerFile{path: path, hdr: hdr, data: data})
		}
		if err := writeLayerFile(x.ctx, path, hdrInfo.Mode(), reader); err != nil {
			return err
		}

	case tar.TypeBlock, tar.TypeChar, tar.TypeFifo:
		if err := handleTarTypeBlockCharFifo(hdr, path); err != nil {
			return err
		}

	case tar.TypeLink:
		// The target may be a file a worker is writing, or a symlink.
		if err := x.finish(); err != nil {
			return err
		}
		x.changed()
		targetPath, err := hardlinkRootPath(x.root, hdr.Linkname)
		if err != nil {
			return err
		}

		if err := os.Link(targetPath, path); err != nil {
			return err
		}

	case tar.TypeSymlink:
		x.changed()
		if err := os.Symlink(hdr.Linkname, path); err != nil {
			return err
		}

	case tar.TypeXGlobalHeader:
		log.G(x.ctx).Debug("PAX Global Extended Headers found and ignored")
		return nil

	default:
		return fmt.Errorf("unhandled tar header type %d", hdr.Typeflag)
	}

	if err := setTarMetadata(x.ctx, path, hdr); err != nil {
		return err
	}
	if hdr.Typeflag == tar.TypeDir {
		x.dirs[path] = struct{}{}
	}
	return nil
}

// beforeLayerFileWrite runs on a worker before it writes each file, and an
// error from it is the write's. Tests slow or fail the workers with it.
var beforeLayerFileWrite = func(string) error { return nil }

// layerFile is a regular file for a worker to write.
type layerFile struct {
	path string
	hdr  *tar.Header
	data []byte
}

// layerFileWriters writes regular files handed to it, on
// layerApplyWorkers goroutines. After the first failure, or once the context
// is done, the rest are skipped, and the failure is returned by failed,
// finish and stop.
type layerFileWriters struct {
	ctx     context.Context
	files   chan layerFile
	pending sync.WaitGroup
	workers sync.WaitGroup

	mu  sync.Mutex
	err error
}

func startLayerFileWriters(ctx context.Context) *layerFileWriters {
	w := &layerFileWriters{ctx: ctx, files: make(chan layerFile, 2*layerApplyWorkers)}
	for range layerApplyWorkers {
		w.workers.Go(func() {
			for f := range w.files {
				if w.failed() == nil {
					err := ctx.Err()
					if err == nil {
						err = beforeLayerFileWrite(f.path)
					}
					if err == nil {
						err = writeQueuedLayerFile(ctx, f)
					}
					if err != nil {
						w.fail(err)
					}
				}
				w.pending.Done()
			}
		})
	}
	return w
}

func (w *layerFileWriters) write(f layerFile) error {
	if err := w.failed(); err != nil {
		return err
	}
	w.pending.Add(1)
	select {
	case w.files <- f:
		return nil
	case <-w.ctx.Done():
		w.pending.Done()
		return w.ctx.Err()
	}
}

func (w *layerFileWriters) fail(err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err == nil {
		w.err = err
	}
}

func (w *layerFileWriters) failed() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.err
}

func (w *layerFileWriters) finish() error {
	w.pending.Wait()
	return w.failed()
}

func (w *layerFileWriters) stop() error {
	close(w.files)
	w.workers.Wait()
	return w.failed()
}

func writeQueuedLayerFile(ctx context.Context, f layerFile) error {
	file, err := openFile(f.path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.hdr.FileInfo().Mode())
	if err != nil {
		return err
	}
	_, err = file.Write(f.data)
	if err1 := file.Close(); err == nil {
		err = err1
	}
	if err != nil {
		return err
	}
	return setTarMetadata(ctx, f.path, f.hdr)
}

func writeLayerFile(ctx context.Context, path string, mode os.FileMode, reader io.Reader) error {
	file, err := openFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	_, err = copyBuffered(ctx, file, reader)
	if err1 := file.Close(); err == nil {
		err = err1
	}
	return err
}

// The rest is containerd v2.2.5's pkg/archive (Apache License 2.0), which
// doesn't export it.

const (
	whiteoutPrefix    = ".wh."
	whiteoutOpaqueDir = whiteoutPrefix + whiteoutPrefix + ".opq"
	paxSchilyXattr    = "SCHILY.xattr."
	userXattrPrefix   = "user."
)

var (
	minTime = time.Unix(0, 0)
	maxTime = time.Unix(0, 1<<63-1)
)

func boundTime(t time.Time) time.Time {
	if t.Before(minTime) || t.After(maxTime) {
		return minTime
	}

	return t
}

func latestTime(t1, t2 time.Time) time.Time {
	if t1.Before(t2) {
		return t2
	}
	return t1
}

// setTarMetadata is the end of containerd's createTarFile: owner, extended
// attributes, mode and times.
func setTarMetadata(ctx context.Context, path string, hdr *tar.Header) error {
	hdrInfo := hdr.FileInfo()

	if err := os.Lchown(path, hdr.Uid, hdr.Gid); err != nil {
		return fmt.Errorf("failed to Lchown %q for UID %d, GID %d: %w", path, hdr.Uid, hdr.Gid, err)
	}

	for key, value := range hdr.PAXRecords {
		if strings.HasPrefix(key, paxSchilyXattr) {
			key = key[len(paxSchilyXattr):]
			if err := setxattr(path, key, value); err != nil {
				if errors.Is(err, syscall.EPERM) && strings.HasPrefix(key, userXattrPrefix) {
					// In the user.* namespace, only regular files and directories can have extended attributes.
					// See https://man7.org/linux/man-pages/man7/xattr.7.html for details.
					if fi, err := os.Lstat(path); err == nil && (!fi.Mode().IsRegular() && !fi.Mode().IsDir()) {
						log.G(ctx).WithError(err).Warnf("ignored xattr %s in archive", key)
						continue
					}
				}
				if errors.Is(err, syscall.ENOTSUP) {
					log.G(ctx).WithError(err).Warnf("ignored xattr %s in archive", key)
					continue
				}
				return fmt.Errorf("failed to setxattr %q for key %q: %w", path, key, err)
			}
		}
	}

	// call lchmod after lchown since lchown can modify the file mode
	if err := lchmod(path, hdrInfo.Mode()); err != nil {
		return err
	}

	return chtimes(path, boundTime(latestTime(hdr.AccessTime, hdr.ModTime)), boundTime(hdr.ModTime))
}

func validateWhiteout(path string) error {
	base := filepath.Base(path)
	dir := filepath.Dir(path)

	if base == whiteoutOpaqueDir {
		return nil
	}

	if strings.HasPrefix(base, whiteoutPrefix) {
		originalBase := base[len(whiteoutPrefix):]
		originalPath := filepath.Join(dir, originalBase)

		// Ensure originalPath is under dir
		if dir[len(dir)-1] != filepath.Separator {
			dir += string(filepath.Separator)
		}
		if !strings.HasPrefix(originalPath, dir) {
			return fmt.Errorf("invalid whiteout name: %v: invalid archive", base)
		}
	}
	return nil
}

// hardlinkRootPath returns target linkname, evaluating and bounding any
// symlink to the parent directory.
func hardlinkRootPath(root, linkname string) (string, error) {
	ppath, base := filepath.Split(linkname)
	ppath, err := fs.RootPath(root, ppath)
	if err != nil {
		return "", err
	}

	targetPath := filepath.Join(ppath, base)
	if !strings.HasPrefix(targetPath, root) {
		targetPath = root
	}
	return targetPath, nil
}

func copyBuffered(ctx context.Context, dst io.Writer, src io.Reader) (written int64, err error) {
	buf := make([]byte, 32*1024)
	for {
		select {
		case <-ctx.Done():
			return written, ctx.Err()
		default:
		}

		nr, er := src.Read(buf)
		if nr > 0 {
			nw, ew := dst.Write(buf[0:nr])
			if nw > 0 {
				written += int64(nw)
			}
			if ew != nil {
				err = ew
				break
			}
			if nr != nw {
				err = io.ErrShortWrite
				break
			}
		}
		if er != nil {
			if er != io.EOF {
				err = er
			}
			break
		}
	}
	return written, err
}

func openFile(name string, flag int, perm os.FileMode) (*os.File, error) {
	f, err := os.OpenFile(name, flag, perm)
	if err != nil {
		return nil, err
	}
	// Call chmod to avoid permission mask
	if err := f.Chmod(perm); err != nil {
		f.Close()
		return nil, err
	}
	return f, err
}

func mkdir(path string, perm os.FileMode) error {
	if err := os.Mkdir(path, perm); err != nil {
		return err
	}
	// Only final created directory gets explicit permission
	// call to avoid permission mask
	return os.Chmod(path, perm)
}

func handleTarTypeBlockCharFifo(hdr *tar.Header, path string) error {
	mode := uint32(hdr.Mode & 07777)
	switch hdr.Typeflag {
	case tar.TypeBlock:
		mode |= unix.S_IFBLK
	case tar.TypeChar:
		mode |= unix.S_IFCHR
	case tar.TypeFifo:
		mode |= unix.S_IFIFO
	}

	return unix.Mknod(path, mode, int(unix.Mkdev(uint32(hdr.Devmajor), uint32(hdr.Devminor))))
}

func lchmod(path string, mode os.FileMode) error {
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}

	if fi.Mode()&os.ModeSymlink == 0 {
		if err := os.Chmod(path, mode); err != nil {
			return err
		}
	}
	return nil
}

func chtimes(path string, atime, mtime time.Time) error {
	var utimes [2]unix.Timespec
	utimes[0] = unix.NsecToTimespec(atime.UnixNano())
	utimes[1] = unix.NsecToTimespec(mtime.UnixNano())

	if err := unix.UtimesNanoAt(unix.AT_FDCWD, path, utimes[0:], unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return fmt.Errorf("failed call to UtimesNanoAt for %s: %w", path, err)
	}

	return nil
}

func setxattr(path, key, value string) error {
	// Do not set trusted attributes
	if strings.HasPrefix(key, "trusted.") {
		return fmt.Errorf("admin attributes from archive not supported: %w", unix.ENOTSUP)
	}
	return unix.Lsetxattr(path, key, []byte(value), 0)
}

func copyDirInfo(fi os.FileInfo, path string) error {
	st := fi.Sys().(*syscall.Stat_t)
	if err := os.Lchown(path, int(st.Uid), int(st.Gid)); err != nil {
		if os.IsPermission(err) {
			// Normally if uid/gid are the same this would be a no-op, but some
			// filesystems may still return EPERM... for instance NFS does this.
			// In such a case, this is not an error.
			if dstStat, err2 := os.Lstat(path); err2 == nil {
				st2 := dstStat.Sys().(*syscall.Stat_t)
				if st.Uid == st2.Uid && st.Gid == st2.Gid {
					err = nil
				}
			}
		}
		if err != nil {
			return fmt.Errorf("failed to chown %s: %w", path, err)
		}
	}

	if err := os.Chmod(path, fi.Mode()); err != nil {
		return fmt.Errorf("failed to chmod %s: %w", path, err)
	}

	timespec := []unix.Timespec{
		unix.NsecToTimespec(syscall.TimespecToNsec(fs.StatAtime(st))),
		unix.NsecToTimespec(syscall.TimespecToNsec(fs.StatMtime(st))),
	}
	if err := unix.UtimesNanoAt(unix.AT_FDCWD, path, timespec, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return fmt.Errorf("failed to utime %s: %w", path, err)
	}

	return nil
}

func copyUpXAttrs(dst, src string) error {
	xattrKeys, err := sysx.LListxattr(src)
	if err != nil {
		if err == unix.ENOTSUP || err == sysx.ENODATA {
			return nil
		}
		return fmt.Errorf("failed to list xattrs on %s: %w", src, err)
	}
	for _, xattr := range xattrKeys {
		// Do not copy up trusted attributes
		if strings.HasPrefix(xattr, "trusted.") {
			continue
		}
		data, err := sysx.LGetxattr(src, xattr)
		if err != nil {
			if err == unix.ENOTSUP || err == sysx.ENODATA {
				continue
			}
			return fmt.Errorf("failed to get xattr %q on %s: %w", xattr, src, err)
		}
		if err := lsetxattrCreate(dst, xattr, data); err != nil {
			return fmt.Errorf("failed to set xattr %q on %s: %w", xattr, dst, err)
		}
	}

	return nil
}

// lsetxattrCreate wraps unix.Lsetxattr, passes the unix.XATTR_CREATE flag on
// supported operating systems,and ignores appropriate errors
func lsetxattrCreate(link string, attr string, data []byte) error {
	err := unix.Lsetxattr(link, attr, data, unix.XATTR_CREATE)
	if err == unix.ENOTSUP || err == unix.ENODATA || err == unix.EEXIST {
		return nil
	}
	return err
}
