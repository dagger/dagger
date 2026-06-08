//go:build linux

package layercopy

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/continuity/sysx"
	"golang.org/x/sys/unix"
)

type inode struct {
	dev uint64
	ino uint64
}

type destination struct {
	viewRoot  string
	writeRoot string
	overlay   bool
	userxattr bool

	sourceLinks map[inode]string
	crossLinks  map[inode]struct{}
}

func newDestination(m Mount) (*destination, error) {
	if m.Root == "" {
		return nil, fmt.Errorf("destination root is empty")
	}

	d := &destination{
		viewRoot:    m.Root,
		writeRoot:   m.Root,
		sourceLinks: map[inode]string{},
		crossLinks:  map[inode]struct{}{},
	}
	if m.Mount == nil {
		return d, nil
	}

	switch {
	case m.Mount.Type == "bind" || m.Mount.Type == "rbind":
		d.writeRoot = m.Mount.Source
	case isOverlayMount(m.Mount):
		for _, opt := range m.Mount.Options {
			switch {
			case strings.HasPrefix(opt, "upperdir="):
				d.writeRoot = strings.TrimPrefix(opt, "upperdir=")
			case opt == "userxattr":
				d.userxattr = true
			}
		}
		if d.writeRoot == "" || d.writeRoot == m.Root {
			return nil, fmt.Errorf("overlay destination missing upperdir option")
		}
		d.overlay = true
	default:
		return nil, fmt.Errorf("unsupported destination mount type %q", m.Mount.Type)
	}

	return d, nil
}

func (d *destination) mkdir(destPath string, opts CopyOptions) error {
	destPath = cleanContainerPath(destPath)
	if opts.ReplaceExisting {
		destInfo, exists, err := d.statView(destPath)
		if err != nil {
			return err
		}
		if exists && !destInfo.IsDir() {
			realPath, err := d.realPath(destPath)
			if err != nil {
				return err
			}
			if err := os.RemoveAll(realPath); err != nil {
				return err
			}
			if d.overlay {
				if err := os.Mkdir(realPath, mkdirMode(nil, opts)); err != nil && !os.IsExist(err) {
					return err
				}
				if err := d.markOpaque(realPath); err != nil {
					return err
				}
				return d.applyMetadataPath(realPath, nil, opts)
			}
		}
	}

	rel, created, err := d.ensureDir(destPath, nil, opts, false)
	if err != nil {
		return err
	}
	if created || opts.Chown != nil || opts.Mode != nil {
		return d.applyMetadataPath(filepath.Join(d.writeRoot, rel), nil, opts)
	}
	return nil
}

func (d *destination) materializeDirPath(destPath string) (string, error) {
	rel, _, err := d.ensureDir(destPath, nil, CopyOptions{}, false)
	if err != nil {
		return "", err
	}
	return filepath.Join(d.writeRoot, rel), nil
}

func (d *destination) ensureParent(destPath string) (string, error) {
	parent := filepath.Dir(cleanContainerPath(destPath))
	rel, _, err := d.ensureDir(parent, nil, CopyOptions{}, false)
	return rel, err
}

func (d *destination) ensureDir(destPath string, src *sourceEntry, opts CopyOptions, overwriteMetadata bool) (string, bool, error) {
	destPath = cleanContainerPath(destPath)
	if destPath == "/" {
		return "", false, nil
	}

	if rel, exists, err := d.ensureExistingDir(destPath, src, opts, overwriteMetadata); err != nil || exists {
		return rel, false, err
	}

	return d.createDir(destPath, src, opts)
}

func (d *destination) ensureExistingDir(destPath string, src *sourceEntry, opts CopyOptions, overwriteMetadata bool) (string, bool, error) {
	viewPath, err := rootPath(d.viewRoot, destPath, true)
	if err != nil {
		if os.IsNotExist(err) || isNotDir(err) {
			return "", false, nil
		}
		return "", false, err
	}

	info, err := os.Stat(viewPath)
	if err != nil {
		if os.IsNotExist(err) || isNotDir(err) {
			return "", false, nil
		}
		return "", false, err
	}
	if !info.IsDir() {
		return d.ensureOverlayReplacementDir(destPath, viewPath, src, opts, overwriteMetadata)
	}

	rel, err := filepath.Rel(d.viewRoot, viewPath)
	if err != nil {
		return "", false, err
	}
	rel = cleanRel(rel)
	// Recurse on the parent of the resolved path, not destPath, to handle
	// symlinks on dest paths.
	if parent := filepath.Dir(cleanContainerPath(rel)); parent != "/" && parent != "." {
		if _, _, err := d.ensureDir(parent, nil, CopyOptions{}, false); err != nil {
			return "", false, err
		}
	}
	realPath, materialized, err := d.materializeExistingDir(rel, viewPath)
	if err != nil {
		return "", false, err
	}
	if overwriteMetadata || materialized {
		if err := d.applyMetadataPath(realPath, src, opts); err != nil {
			return "", false, err
		}
	}
	return rel, true, nil
}

func (d *destination) ensureOverlayReplacementDir(destPath, viewPath string, src *sourceEntry, opts CopyOptions, overwriteMetadata bool) (string, bool, error) {
	if !d.overlay {
		return "", false, fmt.Errorf("cannot copy directory to non-directory %q", destPath)
	}

	rel, err := filepath.Rel(d.viewRoot, viewPath)
	if err != nil {
		return "", false, err
	}
	rel = cleanRel(rel)
	realPath := filepath.Join(d.writeRoot, rel)
	upperInfo, err := os.Lstat(realPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, fmt.Errorf("cannot copy directory to non-directory %q", destPath)
		}
		return "", false, err
	}
	if !upperInfo.IsDir() {
		return "", false, fmt.Errorf("cannot copy directory to non-directory %q", destPath)
	}
	if err := d.markOpaque(realPath); err != nil {
		return "", false, err
	}
	if overwriteMetadata {
		if err := d.applyMetadataPath(realPath, src, opts); err != nil {
			return "", false, err
		}
	}
	return rel, true, nil
}

func (d *destination) createDir(destPath string, src *sourceEntry, opts CopyOptions) (string, bool, error) {
	parentRel, _, err := d.ensureDir(filepath.Dir(destPath), nil, CopyOptions{}, false)
	if err != nil {
		return "", false, err
	}
	rel := filepath.Join(parentRel, filepath.Base(destPath))
	realPath := filepath.Join(d.writeRoot, rel)
	mode := mkdirMode(src, opts)
	if err := os.Mkdir(realPath, mode); err != nil {
		if !os.IsExist(err) {
			return "", false, err
		}
		if err := d.replaceCreateDir(realPath, mode, opts); err != nil {
			return "", false, err
		}
	}
	if err := d.applyMetadataPath(realPath, src, opts); err != nil {
		return "", false, err
	}
	return rel, true, nil
}

func (d *destination) replaceCreateDir(realPath string, mode os.FileMode, opts CopyOptions) error {
	if !opts.ReplaceExisting {
		return nil
	}
	info, err := os.Lstat(realPath)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return nil
	}
	if err := os.RemoveAll(realPath); err != nil {
		return err
	}
	if err := os.Mkdir(realPath, mode); err != nil && !os.IsExist(err) {
		return err
	}
	if d.overlay {
		return d.markOpaque(realPath)
	}
	return nil
}

func mkdirMode(src *sourceEntry, opts CopyOptions) os.FileMode {
	mode := os.FileMode(0o755)
	if src != nil {
		mode = src.Info.Mode().Perm()
	}
	if opts.Mode != nil {
		mode = *opts.Mode
	}
	return mode
}

func (d *destination) materializeExistingDir(rel, viewPath string) (string, bool, error) {
	realPath := filepath.Join(d.writeRoot, rel)
	if info, err := os.Lstat(realPath); err == nil {
		if !info.IsDir() {
			return "", false, fmt.Errorf("destination upper path %q exists and is not a directory", realPath)
		}
		return realPath, false, nil
	} else if !os.IsNotExist(err) {
		return "", false, err
	}

	info, err := os.Lstat(viewPath)
	if err != nil {
		return "", false, err
	}
	if err := os.Mkdir(realPath, info.Mode().Perm()); err != nil && !os.IsExist(err) {
		return "", false, err
	}
	if err := copyMetadata(realPath, viewPath, info, nil, nil, false, nil); err != nil {
		return "", false, err
	}
	return realPath, true, nil
}

func (d *destination) realPath(destPath string) (string, error) {
	destPath = cleanContainerPath(destPath)
	parentRel, err := d.ensureParent(destPath)
	if err != nil {
		return "", err
	}
	rel := filepath.Join(parentRel, filepath.Base(destPath))
	return filepath.Join(d.writeRoot, rel), nil
}

func (d *destination) statView(destPath string) (os.FileInfo, bool, error) {
	viewPath, err := rootPath(d.viewRoot, destPath, true)
	if err != nil {
		if os.IsNotExist(err) || isNotDir(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	info, err := os.Stat(viewPath)
	if err == nil {
		return info, true, nil
	}
	if os.IsNotExist(err) || isNotDir(err) {
		return nil, false, nil
	}
	return nil, false, err
}

func (d *destination) removeForReplace(destPath string, srcInfo os.FileInfo, opts CopyOptions) error {
	if !opts.ReplaceExisting {
		return nil
	}

	destInfo, exists, err := d.statView(destPath)
	if err != nil || !exists {
		return err
	}
	if srcInfo.IsDir() && destInfo.IsDir() {
		return nil
	}

	realPath, err := d.realPath(destPath)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(realPath); err != nil {
		return err
	}
	if d.overlay && srcInfo.IsDir() && !destInfo.IsDir() {
		if err := os.Mkdir(realPath, srcInfo.Mode().Perm()); err != nil && !os.IsExist(err) {
			return err
		}
		if err := d.markOpaque(realPath); err != nil {
			return err
		}
	}
	return nil
}

func (d *destination) applyMetadataPath(dstPath string, src *sourceEntry, opts CopyOptions) error {
	var info os.FileInfo
	var srcPath string
	if src != nil {
		info = src.Info
		srcPath = src.RealPath
	}
	return copyMetadata(dstPath, srcPath, info, opts.Chown, opts.Mode, d.userxattr, opts.XAttrErrorHandler)
}

func (d *destination) flush() error {
	return nil
}

func (d *destination) usage() (snapshots.Usage, error) {
	seen := map[inode]struct{}{}
	var usage snapshots.Usage
	err := filepath.WalkDir(d.writeRoot, func(path string, ent fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := ent.Info()
		if err != nil {
			return err
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return fmt.Errorf("unexpected stat type %T", info.Sys())
		}
		ino := statInode(st)
		if _, ok := seen[ino]; ok {
			return nil
		}
		seen[ino] = struct{}{}
		if _, ok := d.crossLinks[ino]; ok {
			return nil
		}
		usage.Inodes++
		usage.Size += st.Blocks * 512
		return nil
	})
	return usage, err
}

func copyMetadata(dstPath, srcPath string, srcInfo os.FileInfo, chown *Ownership, modeOverride *os.FileMode, userxattr bool, xattrErrorHandler XAttrErrorHandler) error {
	if srcInfo != nil {
		st, ok := srcInfo.Sys().(*syscall.Stat_t)
		if !ok {
			return fmt.Errorf("unexpected stat type %T", srcInfo.Sys())
		}
		uid, gid := int(st.Uid), int(st.Gid)
		if chown != nil {
			uid, gid = chown.UID, chown.GID
		}
		if err := os.Lchown(dstPath, uid, gid); err != nil {
			return err
		}

		mode := srcInfo.Mode()
		if modeOverride != nil {
			mode = *modeOverride
		}
		if srcInfo.Mode()&os.ModeSymlink == 0 {
			if err := os.Chmod(dstPath, mode); err != nil {
				return err
			}
		}

		if srcPath != "" {
			if err := copyXattrs(dstPath, srcPath, userxattr, xattrErrorHandler); err != nil {
				return err
			}
		}

		atime := unix.Timespec{Sec: st.Atim.Sec, Nsec: st.Atim.Nsec}
		mtime := unix.Timespec{Sec: st.Mtim.Sec, Nsec: st.Mtim.Nsec}
		if srcInfo.IsDir() {
			return unix.UtimesNanoAt(unix.AT_FDCWD, dstPath, []unix.Timespec{atime, mtime}, unix.AT_SYMLINK_NOFOLLOW)
		}
		return unix.UtimesNanoAt(unix.AT_FDCWD, dstPath, []unix.Timespec{atime, mtime}, unix.AT_SYMLINK_NOFOLLOW)
	}

	if chown != nil {
		if err := os.Lchown(dstPath, chown.UID, chown.GID); err != nil {
			return err
		}
	}
	if modeOverride != nil {
		if err := os.Chmod(dstPath, *modeOverride); err != nil {
			return err
		}
	}
	return nil
}

func copyXattrs(dstPath, srcPath string, _ bool, xattrErrorHandler XAttrErrorHandler) error {
	xattrs, err := sysx.LListxattr(srcPath)
	if err != nil {
		if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.ENODATA) {
			return nil
		}
		if xattrErrorHandler != nil {
			return xattrErrorHandler(dstPath, srcPath, "", err)
		}
		return fmt.Errorf("failed to list xattrs on %s: %w", srcPath, err)
	}
	for _, xattr := range xattrs {
		if xattr == "trusted.overlay.opaque" || xattr == "user.overlay.opaque" {
			continue
		}
		val, err := sysx.LGetxattr(srcPath, xattr)
		if err != nil {
			if errors.Is(err, unix.ENODATA) {
				continue
			}
			if xattrErrorHandler != nil {
				if err := xattrErrorHandler(dstPath, srcPath, xattr, err); err != nil {
					return err
				}
				continue
			}
			return fmt.Errorf("failed to get xattr %q on %s: %w", xattr, srcPath, err)
		}
		if err := sysx.LSetxattr(dstPath, xattr, val, 0); err != nil && xattrErrorHandler != nil {
			if err := xattrErrorHandler(dstPath, srcPath, xattr, err); err != nil {
				return err
			}
		}
	}
	return nil
}

func (d *destination) markOpaque(path string) error {
	return sysx.LSetxattr(path, opaqueXattr(d.userxattr), []byte{'y'}, 0)
}

func opaqueXattr(userxattr bool) string {
	if userxattr {
		return "user.overlay.opaque"
	}
	return "trusted.overlay.opaque"
}

func overlayLayers(m *mount.Mount) ([]string, error) {
	var upper string
	var lower []string
	for _, opt := range m.Options {
		switch {
		case strings.HasPrefix(opt, "upperdir="):
			upper = strings.TrimPrefix(opt, "upperdir=")
		case strings.HasPrefix(opt, "lowerdir="):
			lower = strings.Split(strings.TrimPrefix(opt, "lowerdir="), ":")
			slices.Reverse(lower)
		case strings.HasPrefix(opt, "workdir="), opt == "index=off", opt == "userxattr", strings.HasPrefix(opt, "redirect_dir="), opt == "volatile":
		default:
			return nil, fmt.Errorf("unknown overlay option %q", opt)
		}
	}
	if upper != "" {
		lower = append(lower, upper)
	}
	if len(lower) == 0 {
		return nil, fmt.Errorf("overlay mount has no layers")
	}
	return lower, nil
}

func isOverlayMount(m *mount.Mount) bool {
	if m == nil {
		return false
	}
	return m.Type == "overlay" || strings.HasPrefix(m.Type, "fuse-overlayfs")
}

func statInode(st *syscall.Stat_t) inode {
	if st == nil {
		return inode{}
	}
	return inode{dev: st.Dev, ino: st.Ino}
}

func cleanContainerPath(p string) string {
	p = filepath.Join("/", p)
	if p == "." {
		return "/"
	}
	return p
}

func cleanRel(p string) string {
	p = filepath.Clean(p)
	if p == "." || p == string(filepath.Separator) {
		return ""
	}
	return strings.TrimPrefix(p, string(filepath.Separator))
}
