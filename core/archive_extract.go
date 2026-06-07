package core

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/containerd/containerd/v2/core/mount"
	containerdfs "github.com/containerd/continuity/fs"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	bkclient "github.com/dagger/dagger/internal/buildkit/client"
	"github.com/ulikunitz/xz"

	"github.com/dagger/dagger/dagql"
)

const (
	maxZipEntrySize = uint64(1<<63 - 1)
	tarTypeRegA     = byte(0)
)

type DirectoryFileExtractLazy struct {
	LazyState
	Parent          dagql.ObjectResult[*File]
	StripComponents int
}

type persistedDirectoryFileExtractLazy struct {
	ParentResultID  uint64 `json:"parentResultID"`
	StripComponents int    `json:"stripComponents,omitempty"`
}

func (lazy *DirectoryFileExtractLazy) Evaluate(ctx context.Context, dir *Directory) error {
	return lazy.LazyState.Evaluate(ctx, "File.extract", func(ctx context.Context) error {
		return dir.ExtractArchive(ctx, lazy.Parent, lazy.StripComponents)
	})
}

func (lazy *DirectoryFileExtractLazy) AttachDependencies(ctx context.Context, attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	parent, err := attachFileResult(attach, lazy.Parent, "attach file extract parent")
	if err != nil {
		return nil, err
	}
	lazy.Parent = parent
	return []dagql.AnyResult{parent}, nil
}

func (lazy *DirectoryFileExtractLazy) EncodePersisted(ctx context.Context, cache dagql.PersistedObjectCache) (json.RawMessage, error) {
	parentID, err := encodePersistedObjectRef(cache, lazy.Parent, "file extract parent")
	if err != nil {
		return nil, err
	}
	return json.Marshal(persistedDirectoryFileExtractLazy{
		ParentResultID:  parentID,
		StripComponents: lazy.StripComponents,
	})
}

func (dir *Directory) ExtractArchive(ctx context.Context, parent dagql.ObjectResult[*File], stripComponents int) error {
	if stripComponents < 0 {
		return fmt.Errorf("stripComponents must be greater than or equal to 0")
	}

	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return err
	}
	if err := cache.Evaluate(ctx, parent); err != nil {
		return err
	}

	srcPath, err := parent.Self().File.GetOrEval(ctx, parent.Result)
	if err != nil {
		return fmt.Errorf("failed to get source file path: %w", err)
	}
	srcRef, err := parent.Self().Snapshot.GetOrEval(ctx, parent.Result)
	if err != nil {
		return fmt.Errorf("failed to get source file snapshot: %w", err)
	}
	if srcRef == nil {
		return fmt.Errorf("file extract source snapshot is nil")
	}

	query, err := CurrentQuery(ctx)
	if err != nil {
		return err
	}
	newRef, err := query.SnapshotManager().New(
		ctx,
		nil,
		bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
		bkcache.WithDescription(fmt.Sprintf("file.extract %s", filepath.Base(srcPath))),
	)
	if err != nil {
		return err
	}
	defer newRef.Release(context.WithoutCancel(ctx))

	if err := MountRef(ctx, newRef, func(destRoot string, _ *mount.Mount) error {
		return MountRef(ctx, srcRef, func(srcRoot string, _ *mount.Mount) error {
			resolvedSrcPath, err := containerdfs.RootPath(srcRoot, srcPath)
			if err != nil {
				return err
			}
			return extractArchiveFile(resolvedSrcPath, destRoot, stripComponents)
		}, mountRefAsReadOnly)
	}); err != nil {
		return err
	}

	snapshot, err := newRef.Commit(ctx)
	if err != nil {
		return err
	}
	dir.Dir.setValue("/")
	dir.Snapshot.setValue(snapshot)
	return nil
}

func extractArchiveFile(srcPath, destRoot string, stripComponents int) error {
	if hasZipHeader(srcPath) {
		if err := extractZipArchive(srcPath, destRoot, stripComponents); err != nil {
			return fmt.Errorf("extract zip archive: %w", err)
		}
		return nil
	}

	if err := extractTarArchive(srcPath, destRoot, stripComponents); err != nil {
		return fmt.Errorf("extract tar archive: %w", err)
	}
	return nil
}

func hasZipHeader(srcPath string) bool {
	f, err := os.Open(srcPath)
	if err != nil {
		return false
	}
	defer f.Close()

	var hdr [4]byte
	if _, err := io.ReadFull(f, hdr[:]); err != nil {
		return false
	}
	return hdr == [4]byte{'P', 'K', 0x03, 0x04} ||
		hdr == [4]byte{'P', 'K', 0x05, 0x06} ||
		hdr == [4]byte{'P', 'K', 0x07, 0x08}
}

func extractTarArchive(srcPath, destRoot string, stripComponents int) error {
	if err := os.MkdirAll(destRoot, 0o755); err != nil {
		return err
	}

	src, err := openTarArchive(srcPath)
	if err != nil {
		return err
	}
	defer src.Close()

	return extractTarStream(src, destRoot, stripComponents)
}

type archiveReadCloser struct {
	io.Reader
	close func() error
}

func (r archiveReadCloser) Close() error {
	if r.close == nil {
		return nil
	}
	return r.close()
}

func openTarArchive(srcPath string) (io.ReadCloser, error) {
	file, err := os.Open(srcPath)
	if err != nil {
		return nil, err
	}

	buf := bufio.NewReader(file)
	magic, err := buf.Peek(6)
	if err != nil && !errors.Is(err, io.EOF) {
		_ = file.Close()
		return nil, err
	}

	closeFile := func() error {
		return file.Close()
	}
	if len(magic) >= 2 && magic[0] == 0x1f && magic[1] == 0x8b {
		gz, err := gzip.NewReader(buf)
		if err != nil {
			_ = file.Close()
			return nil, err
		}
		return archiveReadCloser{
			Reader: gz,
			close: func() error {
				err := gz.Close()
				if closeErr := file.Close(); err == nil {
					err = closeErr
				}
				return err
			},
		}, nil
	}
	if len(magic) >= 6 && magic[0] == 0xfd && magic[1] == '7' && magic[2] == 'z' && magic[3] == 'X' && magic[4] == 'Z' && magic[5] == 0x00 {
		xzr, err := xz.NewReader(buf)
		if err != nil {
			_ = file.Close()
			return nil, err
		}
		return archiveReadCloser{Reader: xzr, close: closeFile}, nil
	}
	return archiveReadCloser{Reader: buf, close: closeFile}, nil
}

func extractTarStream(src io.Reader, destRoot string, stripComponents int) error {
	tr := tar.NewReader(src)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag == tar.TypeXGlobalHeader {
			continue
		}

		relPath, ok, err := strippedArchivePath(hdr.Name, stripComponents)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := createExtractedDirectory(destRoot, relPath, tarDirectoryMode(hdr)); err != nil {
				return err
			}
		case tar.TypeReg, tarTypeRegA:
			if err := extractTarRegularFile(tr, hdr, destRoot, relPath); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := extractTarSymlink(hdr, destRoot, relPath); err != nil {
				return err
			}
		case tar.TypeLink:
			if err := extractTarHardlink(hdr, destRoot, relPath, stripComponents); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported tar entry type %q for %q", hdr.Typeflag, hdr.Name)
		}
	}
}

func extractTarRegularFile(src io.Reader, hdr *tar.Header, root, relPath string) error {
	destPath, err := RootPathWithoutFinalSymlink(root, relPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return err
	}
	if err := os.RemoveAll(destPath); err != nil {
		return err
	}

	mode := tarFileMode(hdr)
	dst, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		return err
	}
	if err := dst.Close(); err != nil {
		return err
	}
	if err := os.Chmod(destPath, mode); err != nil {
		return err
	}
	if !hdr.ModTime.IsZero() {
		if err := os.Chtimes(destPath, hdr.ModTime, hdr.ModTime); err != nil {
			return err
		}
	}
	return nil
}

func extractTarSymlink(hdr *tar.Header, root, relPath string) error {
	target := strings.ReplaceAll(hdr.Linkname, "\\", "/")
	if err := validateSymlinkTarget(relPath, target); err != nil {
		return err
	}
	destPath, err := RootPathWithoutFinalSymlink(root, relPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return err
	}
	if err := os.RemoveAll(destPath); err != nil {
		return err
	}
	return os.Symlink(target, destPath)
}

func extractTarHardlink(hdr *tar.Header, root, relPath string, stripComponents int) error {
	targetRelPath, ok, err := strippedArchivePath(hdr.Linkname, stripComponents)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}

	destPath, err := RootPathWithoutFinalSymlink(root, relPath)
	if err != nil {
		return err
	}
	targetPath, err := RootPathWithoutFinalSymlink(root, targetRelPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return err
	}
	if err := os.RemoveAll(destPath); err != nil {
		return err
	}
	return os.Link(targetPath, destPath)
}

func tarFileMode(hdr *tar.Header) fs.FileMode {
	mode := fs.FileMode(hdr.Mode) & os.ModePerm
	if mode == 0 {
		return 0o644
	}
	return mode
}

func tarDirectoryMode(hdr *tar.Header) fs.FileMode {
	mode := fs.FileMode(hdr.Mode) & os.ModePerm
	if mode == 0 {
		return 0o755
	}
	return mode
}

func extractZipArchive(srcPath, destRoot string, stripComponents int) error {
	zr, err := zip.OpenReader(srcPath)
	if err != nil {
		return err
	}
	defer zr.Close()

	if err := os.MkdirAll(destRoot, 0o755); err != nil {
		return err
	}

	for _, entry := range zr.File {
		relPath, ok, err := strippedArchivePath(entry.Name, stripComponents)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}

		info := entry.FileInfo()
		mode := entry.Mode()
		switch {
		case info.IsDir():
			if err := createExtractedDirectory(destRoot, relPath, zipDirectoryMode(mode)); err != nil {
				return err
			}
		case mode&os.ModeSymlink != 0:
			if err := extractZipSymlink(entry, destRoot, relPath); err != nil {
				return err
			}
		case mode.IsRegular() || mode.Type() == 0:
			if err := extractZipRegularFile(entry, destRoot, relPath); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported zip entry type %s for %q", mode.Type(), entry.Name)
		}
	}
	return nil
}

func createExtractedDirectory(root, relPath string, mode fs.FileMode) error {
	destPath, err := RootPathWithoutFinalSymlink(root, relPath)
	if err != nil {
		return err
	}
	if existing, err := os.Lstat(destPath); err == nil {
		if existing.IsDir() && existing.Mode()&os.ModeSymlink == 0 {
			return os.Chmod(destPath, mode)
		}
		if err := os.RemoveAll(destPath); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.MkdirAll(destPath, mode)
}

func extractZipRegularFile(entry *zip.File, root, relPath string) error {
	destPath, err := RootPathWithoutFinalSymlink(root, relPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return err
	}
	if err := os.RemoveAll(destPath); err != nil {
		return err
	}

	rc, err := entry.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	mode := zipFileMode(entry.Mode())
	dst, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if err := copyZipRegularFile(entry, dst, rc); err != nil {
		_ = dst.Close()
		return err
	}
	if err := dst.Close(); err != nil {
		return err
	}
	if err := os.Chmod(destPath, mode); err != nil {
		return err
	}
	modTime := entry.Modified
	if !modTime.IsZero() {
		if err := os.Chtimes(destPath, modTime, modTime); err != nil {
			return err
		}
	}
	return nil
}

func copyZipRegularFile(entry *zip.File, dst io.Writer, src io.Reader) error {
	if entry.UncompressedSize64 > maxZipEntrySize {
		return fmt.Errorf("zip entry %q exceeds maximum supported size", entry.Name)
	}
	if _, err := io.CopyN(dst, src, int64(entry.UncompressedSize64)); err != nil {
		return err
	}

	var extra [1]byte
	n, err := src.Read(extra[:])
	if n > 0 || err == nil {
		return fmt.Errorf("zip entry %q exceeded declared uncompressed size", entry.Name)
	}
	if !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func extractZipSymlink(entry *zip.File, root, relPath string) error {
	rc, err := entry.Open()
	if err != nil {
		return err
	}
	target, err := io.ReadAll(rc)
	closeErr := rc.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	targetPath := string(target)
	if err := validateSymlinkTarget(relPath, targetPath); err != nil {
		return err
	}

	destPath, err := RootPathWithoutFinalSymlink(root, relPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return err
	}
	if err := os.RemoveAll(destPath); err != nil {
		return err
	}
	return os.Symlink(targetPath, destPath)
}

func zipFileMode(mode fs.FileMode) fs.FileMode {
	mode &= os.ModePerm
	if mode == 0 {
		return 0o644
	}
	return mode
}

func zipDirectoryMode(mode fs.FileMode) fs.FileMode {
	mode &= os.ModePerm
	if mode == 0 {
		return 0o755
	}
	return mode
}

func strippedArchivePath(name string, stripComponents int) (string, bool, error) {
	if stripComponents < 0 {
		return "", false, fmt.Errorf("stripComponents must be greater than or equal to 0")
	}
	name = strings.ReplaceAll(name, "\\", "/")
	if name == "" {
		return "", false, nil
	}
	if strings.ContainsRune(name, 0) {
		return "", false, fmt.Errorf("archive entry path contains NUL byte")
	}
	if path.IsAbs(name) {
		return "", false, fmt.Errorf("archive entry path %q must be relative", name)
	}

	cleaned := path.Clean(name)
	if cleaned == "." {
		return "", false, nil
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", false, fmt.Errorf("archive entry path %q escapes extraction root", name)
	}

	components := strings.Split(cleaned, "/")
	if len(components) <= stripComponents {
		return "", false, nil
	}
	stripped := path.Join(components[stripComponents:]...)
	if stripped == "." || stripped == "" {
		return "", false, nil
	}
	if !fs.ValidPath(stripped) {
		return "", false, fmt.Errorf("archive entry path %q is invalid after stripping", name)
	}
	return stripped, true, nil
}

func validateSymlinkTarget(linkPath, target string) error {
	target = strings.ReplaceAll(target, "\\", "/")
	if target == "" {
		return fmt.Errorf("symlink %q has empty target", linkPath)
	}
	if strings.ContainsRune(target, 0) {
		return fmt.Errorf("symlink %q target contains NUL byte", linkPath)
	}
	if path.IsAbs(target) {
		return fmt.Errorf("symlink %q target %q must be relative", linkPath, target)
	}
	cleanedTarget := path.Clean(path.Join(path.Dir(linkPath), target))
	if cleanedTarget == ".." || strings.HasPrefix(cleanedTarget, "../") {
		return fmt.Errorf("symlink %q target %q escapes extraction root", linkPath, target)
	}
	return nil
}
