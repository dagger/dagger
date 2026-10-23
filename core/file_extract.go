package core

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/klauspost/compress/zip"
	"github.com/moby/go-archive"
	"github.com/moby/go-archive/chrootarchive"
	"github.com/moby/go-archive/compression"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sys/unix"

	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	bkclient "github.com/dagger/dagger/internal/buildkit/client"
)

const persistedDirectoryLazyKindExtract = "file.extract"

// DirectoryExtractLazy is the directory an archive file extracts to.
type DirectoryExtractLazy struct {
	LazyState
	Source          dagql.ObjectResult[*File]
	StripComponents int
}

type persistedDirectoryExtractLazy struct {
	SourceResultID  uint64 `json:"sourceResultID"`
	StripComponents int    `json:"stripComponents,omitempty"`
}

func (p *persistedDirectoryExtractLazy) validate() error {
	if p.SourceResultID == 0 {
		return fmt.Errorf("DirectoryExtractLazy: missing sourceResultID")
	}
	return nil
}

func (lazy *DirectoryExtractLazy) Evaluate(ctx context.Context, dir *Directory) error {
	return dir.evaluateLazy(ctx, &lazy.LazyState, "File.extract", func(ctx context.Context) error {
		return dir.Extract(ctx, lazy.Source, lazy.StripComponents)
	})
}

func (lazy *DirectoryExtractLazy) AttachDependencies(ctx context.Context, attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	source, err := attachFileResult(attach, lazy.Source, "attach file extract source")
	if err != nil {
		return nil, err
	}
	lazy.Source = source
	return []dagql.AnyResult{source}, nil
}

func (lazy *DirectoryExtractLazy) EncodePersisted(ctx context.Context, enc *dagql.PersistEncodeContext) (json.RawMessage, error) {
	sourceID, err := encodePersistedObjectRef(enc, lazy.Source, "file extract source")
	if err != nil {
		return nil, err
	}
	return json.Marshal(persistedDirectoryExtractLazy{SourceResultID: sourceID, StripComponents: lazy.StripComponents})
}

func decodeDirectoryExtractLazy(ctx context.Context, dec *dagql.PersistDecodeContext, payload json.RawMessage) (Lazy[*Directory], error) {
	var persisted persistedDirectoryExtractLazy
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted file extract lazy: %w", err)
	}
	if err := persisted.validate(); err != nil {
		return nil, err
	}
	source, err := loadPersistedObjectResultByResultID[*File](ctx, dec, persisted.SourceResultID, "file extract source")
	if err != nil {
		return nil, err
	}
	return &DirectoryExtractLazy{LazyState: NewLazyState(), Source: source, StripComponents: persisted.StripComponents}, nil
}

// Extract sets the directory to the contents of the archive src, a zip or a
// tar (uncompressed, or compressed with gzip, bzip2, xz or zstd), with the
// first stripComponents elements of each entry's path removed.
func (dir *Directory) Extract(ctx context.Context, src dagql.ObjectResult[*File], stripComponents int) error {
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return err
	}
	if err := cache.Evaluate(ctx, src); err != nil {
		return err
	}
	query, err := CurrentQuery(ctx)
	if err != nil {
		return err
	}
	srcPath, err := src.Self().File.GetOrEval(ctx, src.Result)
	if err != nil {
		return err
	}
	newRef, err := query.SnapshotManager().New(ctx, nil,
		bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
		bkcache.WithDescription(fmt.Sprintf("extract %s", path.Base(srcPath))))
	if err != nil {
		return err
	}
	err = MountRef(ctx, newRef, func(root string, _ *mount.Mount) error {
		return src.Self().Mount(ctx, src, func(archivePath string) error {
			return TrimErrPathPrefix(extractArchive(ctx, archivePath, root, stripComponents), root)
		})
	})
	if err != nil {
		return fmt.Errorf("extract %s: %w", path.Base(srcPath), err)
	}
	snapshot, err := newRef.Commit(ctx)
	if err != nil {
		return err
	}
	dir.SetPath("/")
	dir.SetSnapshot(snapshot)
	return nil
}

// extractArchive extracts the archive at archivePath into dest, an empty
// directory, choosing the format from the archive's first bytes.
func extractArchive(ctx context.Context, archivePath, dest string, stripComponents int) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	magic := make([]byte, 4)
	n, err := f.ReadAt(magic, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if isZipMagic(magic[:n]) {
		return extractZip(ctx, f, st.Size(), dest, stripComponents)
	}
	return extractTar(f, dest, stripComponents)
}

// isZipMagic reports whether b starts a zip: a local file header, or the
// end of central directory record an empty zip consists of.
func isZipMagic(b []byte) bool {
	return bytes.Equal(b, []byte("PK\x03\x04")) || bytes.Equal(b, []byte("PK\x05\x06"))
}

// archiveEntryPath is the slash-separated path, relative to the extraction
// root, that an archive entry named name extracts to, with its first strip
// elements removed; "" for an entry that is the root, or that stripping
// removes. As for tar, a leading "/" is dropped; a name that leaves the root
// is an error.
func archiveEntryPath(name string, strip int) (string, error) {
	rel := path.Clean(strings.TrimLeft(name, "/"))
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return "", fmt.Errorf("archive entry %q: path leaves the extraction directory", name)
	}
	if rel == "." {
		return "", nil
	}
	if strip > 0 {
		elems := strings.Split(rel, "/")
		if len(elems) <= strip {
			return "", nil
		}
		rel = strings.Join(elems[strip:], "/")
	}
	return rel, nil
}

// extractZipWorkers bounds the entries of one zip written at once.
const extractZipWorkers = 8

// zip entry creators whose external attributes hold Unix modes.
const (
	zipCreatorUnix   = 3
	zipCreatorMacOSX = 19
)

type zipEntry struct {
	file *zip.File
	path string
	mode fs.FileMode
}

// extractZip extracts the zip r of the given size into dest. Directories
// are made first, then regular files are written several at once, then
// symlinks are made, so no entry is written through a symlink the archive
// itself makes. Permissions and modification times are kept where the zip
// records them; files with none recorded get 0644, directories 0755.
func extractZip(ctx context.Context, r io.ReaderAt, size int64, dest string, strip int) error {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return err
	}
	// A later entry of the same path replaces an earlier one.
	byPath := map[string]*zipEntry{}
	var order []string
	for _, f := range zr.File {
		rel, err := archiveEntryPath(f.Name, strip)
		if err != nil {
			return err
		}
		if rel == "" {
			continue
		}
		mode := f.Mode()
		if _, ok := byPath[rel]; !ok {
			order = append(order, rel)
		}
		byPath[rel] = &zipEntry{file: f, path: rel, mode: mode}
	}

	var dirs, files, symlinks []*zipEntry
	mkdirs := map[string]bool{}
	for _, rel := range order {
		e := byPath[rel]
		switch {
		case e.mode.IsDir():
			dirs = append(dirs, e)
			mkdirs[rel] = true
		case e.mode.IsRegular():
			files = append(files, e)
		case e.mode&fs.ModeSymlink != 0:
			symlinks = append(symlinks, e)
		default:
			return fmt.Errorf("archive entry %q: unsupported file type %s", e.file.Name, e.mode.Type())
		}
		for p := path.Dir(rel); p != "."; p = path.Dir(p) {
			mkdirs[p] = true
		}
	}

	// Parents sort before their children.
	mkdirList := make([]string, 0, len(mkdirs))
	for p := range mkdirs {
		mkdirList = append(mkdirList, p)
	}
	sort.Strings(mkdirList)
	for _, p := range mkdirList {
		if err := os.Mkdir(filepath.Join(dest, p), 0o755); err != nil {
			return err
		}
	}

	// Largest first, so a large entry doesn't start last.
	sort.SliceStable(files, func(i, j int) bool {
		return files[i].file.UncompressedSize64 > files[j].file.UncompressedSize64
	})
	eg, egctx := errgroup.WithContext(ctx)
	eg.SetLimit(extractZipWorkers)
	for _, e := range files {
		eg.Go(func() error {
			if err := egctx.Err(); err != nil {
				return err
			}
			return writeZipFile(e, filepath.Join(dest, e.path))
		})
	}
	if err := eg.Wait(); err != nil {
		return err
	}

	// Every parent is a directory made above, never a symlink.
	for _, e := range symlinks {
		target, err := readZipSymlink(e.file)
		if err != nil {
			return err
		}
		linkPath := filepath.Join(dest, e.path)
		if err := os.Symlink(target, linkPath); err != nil {
			return err
		}
		if err := setSymlinkTime(linkPath, e.file.Modified); err != nil {
			return err
		}
	}

	// Last, as making an entry in a directory changes its time.
	for _, e := range dirs {
		p := filepath.Join(dest, e.path)
		if err := os.Chmod(p, zipEntryPerm(e.file, e.mode, 0o755)); err != nil {
			return err
		}
		if !e.file.Modified.IsZero() {
			if err := os.Chtimes(p, e.file.Modified, e.file.Modified); err != nil {
				return err
			}
		}
	}
	return nil
}

// zipEntryPerm is the permission bits e records, or def if it records
// none: only Unix and macOS creators record them.
func zipEntryPerm(f *zip.File, mode fs.FileMode, def fs.FileMode) fs.FileMode {
	switch f.CreatorVersion >> 8 {
	case zipCreatorUnix, zipCreatorMacOSX:
		if perm := mode.Perm(); perm != 0 {
			return perm
		}
	}
	return def
}

var zipCopyBufPool = sync.Pool{New: func() any {
	buf := make([]byte, 128<<10)
	return &buf
}}

func writeZipFile(e *zipEntry, dst string) (rerr error) {
	r, err := e.file.Open()
	if err != nil {
		return fmt.Errorf("archive entry %q: %w", e.file.Name, err)
	}
	defer r.Close()
	w, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, zipEntryPerm(e.file, e.mode, 0o644))
	if err != nil {
		return err
	}
	defer func() {
		if err := w.Close(); rerr == nil {
			rerr = err
		}
	}()
	buf := zipCopyBufPool.Get().(*[]byte)
	defer zipCopyBufPool.Put(buf)
	if _, err := io.CopyBuffer(w, r, *buf); err != nil {
		return fmt.Errorf("archive entry %q: %w", e.file.Name, err)
	}
	if !e.file.Modified.IsZero() {
		return os.Chtimes(dst, e.file.Modified, e.file.Modified)
	}
	return nil
}

// readZipSymlink reads the target of a symlink entry, which the entry's
// contents hold.
func readZipSymlink(f *zip.File) (string, error) {
	r, err := f.Open()
	if err != nil {
		return "", fmt.Errorf("archive entry %q: %w", f.Name, err)
	}
	defer r.Close()
	// Longer than any valid target, which os.Symlink rejects.
	target, err := io.ReadAll(io.LimitReader(r, unix.PathMax+1))
	if err != nil {
		return "", fmt.Errorf("archive entry %q: %w", f.Name, err)
	}
	return string(target), nil
}

func setSymlinkTime(p string, t time.Time) error {
	if t.IsZero() {
		return nil
	}
	ts := unix.NsecToTimespec(t.UnixNano())
	return unix.UtimesNanoAt(unix.AT_FDCWD, p, []unix.Timespec{ts, ts}, unix.AT_SYMLINK_NOFOLLOW)
}

// extractTar extracts the tar r, uncompressed or compressed, into dest with
// the unpacker Directory.withDirectory's Dockerfile ADD compatibility uses,
// after checking and stripping its entries' paths as extractZip does.
func extractTar(r io.Reader, dest string, strip int) error {
	rdr, err := compression.DecompressStream(r)
	if err != nil {
		return err
	}
	defer rdr.Close()
	pr, pw := io.Pipe()
	go func() {
		pw.CloseWithError(stripTarComponents(rdr, pw, strip))
	}()
	err = chrootarchive.Untar(pr, dest, &archive.TarOptions{BestEffortXattrs: true})
	pr.CloseWithError(errors.New("unpack stopped"))
	return err
}

// stripTarComponents copies the tar r to w with the first strip elements
// of each entry's path, and of each hard link's target, removed, dropping
// entries left with none, as tar --strip-components does.
func stripTarComponents(r io.Reader, w io.Writer, strip int) error {
	tr := tar.NewReader(r)
	tw := tar.NewWriter(w)
	for first := true; ; first = false {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return tw.Close()
		}
		if first && errors.Is(err, tar.ErrHeader) {
			return errors.New("not a zip or tar archive")
		}
		if err != nil {
			return err
		}
		name, err := archiveEntryPath(hdr.Name, strip)
		if err != nil {
			return err
		}
		if name == "" {
			continue
		}
		if hdr.Typeflag == tar.TypeLink {
			target, err := archiveEntryPath(hdr.Linkname, strip)
			if err != nil {
				return err
			}
			if target == "" {
				continue
			}
			hdr.Linkname = target
		}
		if hdr.Typeflag == tar.TypeDir {
			name += "/"
		}
		hdr.Name = name
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if _, err := io.Copy(tw, tr); err != nil {
			return err
		}
	}
}
