//go:build linux
// +build linux

package snapshots

import (
	"context"
	stderrors "errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"

	containerdfs "github.com/containerd/continuity/fs"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/internal/buildkit/client"
	fscopy "github.com/dagger/dagger/internal/fsutil/copy"
)

// accountDatabases are the files a named chown of a file reads from the
// filesystem the file is in.
var accountDatabases = []string{"/etc/passwd", "/etc/group"}

// ExportFileChain exports the file at filePath in sr's filesystem as a chain
// of its own: one layer holding the file, under parents with the source's
// metadata, plus the account databases a named chown of the file reads. A
// File is its content, so the rest of the filesystem it was taken from stays
// behind. A path through a symlink, or to anything but a regular file,
// exports sr's whole chain.
func (sr *immutableRef) ExportFileChain(ctx context.Context, refCfg config.RefConfig, filePath string) (_ *ExportChain, rerr error) {
	if sr == nil {
		return &ExportChain{}, nil
	}
	filePath = path.Clean("/" + filePath)

	srcRoot, _, releaseSrc, err := sr.MountShared(ctx)
	if err != nil {
		return nil, err
	}
	defer releaseSrc()
	own, err := ownsItsContent(srcRoot, filePath)
	if err != nil {
		return nil, err
	}
	if !own {
		return sr.ExportChain(ctx, refCfg)
	}

	// The new snapshot is held by this pin until its chain's own pin holds
	// it, and by that until the chain is released.
	pin, ctx, err := sr.cm.newResourcePin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { rerr = stderrors.Join(rerr, pin.release(ctx)) }()
	mut, err := sr.cm.New(ctx, nil,
		WithRecordType(client.UsageRecordTypeRegular),
		WithDescription(fmt.Sprintf("file %s", filePath)))
	if err != nil {
		return nil, err
	}
	defer func() {
		if mut != nil {
			rerr = stderrors.Join(rerr, mut.Release(context.WithoutCancel(ctx)))
		}
	}()
	mounted, err := mut.Mount(ctx, false)
	if err != nil {
		return nil, err
	}
	mounter := LocalMounter(mounted)
	dstRoot, err := mounter.Mount()
	if err != nil {
		return nil, err
	}
	err = copyOwnContent(ctx, srcRoot, dstRoot, filePath)
	if unmountErr := mounter.Unmount(); err == nil {
		err = unmountErr
	}
	if err != nil {
		return nil, err
	}
	committed, err := mut.Commit(ctx)
	if err != nil {
		return nil, err
	}
	mut = nil
	defer func() { rerr = stderrors.Join(rerr, committed.Release(context.WithoutCancel(ctx))) }()
	return committed.ExportChain(ctx, refCfg)
}

// ownsItsContent reports whether the file at filePath under root can travel
// as its own content: a regular file reached through directories only.
func ownsItsContent(root, filePath string) (bool, error) {
	current := root
	parts := strings.Split(strings.TrimPrefix(filePath, "/"), "/")
	for i, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return false, err
		}
		if i == len(parts)-1 {
			return info.Mode().IsRegular(), nil
		}
		if !info.IsDir() {
			return false, nil
		}
	}
	return false, nil
}

// copyOwnContent copies the file at filePath, and the account databases
// where the source has them, from srcRoot to the empty dstRoot. The
// directories it creates get the source's metadata, so the same file makes
// the same layer.
func copyOwnContent(ctx context.Context, srcRoot, dstRoot, filePath string) error {
	created := map[string]bool{}
	var dirs []string
	mkdirs := func(p string) error {
		var missing []string
		for dir := path.Dir(p); dir != "/" && !created[dir]; dir = path.Dir(dir) {
			missing = append(missing, dir)
		}
		for i := len(missing) - 1; i >= 0; i-- {
			if err := os.Mkdir(filepath.Join(dstRoot, missing[i]), 0o755); err != nil {
				return err
			}
			created[missing[i]] = true
			dirs = append(dirs, missing[i])
		}
		return nil
	}

	if err := mkdirs(filePath); err != nil {
		return err
	}
	if err := fscopy.Copy(ctx, srcRoot, filePath, dstRoot, filePath); err != nil {
		return err
	}
	// A named chown reads these through the filesystem's symlinks, so they
	// are copied from where those lead. Where one is a symlink or a hardlink
	// to the file itself, a later edit or rename of the file changes what a
	// named chown reads locally, but not in this copy.
	for _, db := range accountDatabases {
		if db == filePath {
			continue
		}
		resolved, err := containerdfs.RootPath(srcRoot, db)
		if err != nil {
			return err
		}
		info, err := os.Stat(resolved)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		rel, err := filepath.Rel(srcRoot, resolved)
		if err != nil {
			return err
		}
		if err := mkdirs(db); err != nil {
			return err
		}
		if err := fscopy.Copy(ctx, srcRoot, "/"+filepath.ToSlash(rel), dstRoot, db); err != nil {
			return err
		}
	}

	// Children change their parent's times, so the deepest go first.
	for i := len(dirs) - 1; i >= 0; i-- {
		src, err := containerdfs.RootPath(srcRoot, dirs[i])
		if err != nil {
			return err
		}
		if err := copyDirMetadata(src, filepath.Join(dstRoot, dirs[i])); err != nil {
			return err
		}
	}
	return nil
}

// copyDirMetadata gives the directory dst src's owner, mode and modification
// time.
func copyDirMetadata(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		if err := os.Lchown(dst, int(stat.Uid), int(stat.Gid)); err != nil {
			return err
		}
	}
	if err := os.Chmod(dst, info.Mode()&(os.ModePerm|os.ModeSetuid|os.ModeSetgid|os.ModeSticky)); err != nil {
		return err
	}
	return os.Chtimes(dst, info.ModTime(), info.ModTime())
}
