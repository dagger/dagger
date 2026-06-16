//go:build linux

package layercopy

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"github.com/containerd/containerd/v2/core/snapshots"
	"golang.org/x/sys/unix"
)

type pendingDir struct {
	entry    sourceEntry
	destPath string
}

func NewCopier(dest Mount) (*Copier, error) {
	d, err := newDestination(dest)
	if err != nil {
		return nil, err
	}
	return &Copier{dest: d}, nil
}

func (c *Copier) Copy(ctx context.Context, src Mount, srcPath, destPath string, opts CopyOptions) error {
	s, err := newSource(src)
	if err != nil {
		return err
	}
	m, err := newMatcher(s.root, opts.Filter)
	if err != nil {
		return err
	}
	return c.copy(ctx, s, m, srcPath, destPath, opts)
}

func (c *Copier) CopyFile(ctx context.Context, src Mount, srcPath, destPath string, opts CopyOptions) error {
	s, err := newSource(src)
	if err != nil {
		return err
	}
	return c.copyFile(ctx, s, srcPath, destPath, opts)
}

func (c *Copier) Mkdir(ctx context.Context, destPath string, opts CopyOptions) error {
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	default:
	}
	return c.dest.mkdir(destPath, opts)
}

func (c *Copier) MaterializeDestDir(ctx context.Context, destPath string) (string, error) {
	select {
	case <-ctx.Done():
		return "", context.Cause(ctx)
	default:
	}
	return c.dest.materializeDirPath(destPath)
}

func (c *Copier) Close() error {
	return c.dest.flush()
}

func (c *Copier) Usage() (snapshots.Usage, error) {
	if err := c.dest.flush(); err != nil {
		return snapshots.Usage{}, err
	}
	return c.dest.usage()
}

func (c *Copier) copy(ctx context.Context, src *source, matcher *matcher, srcPath, destPath string, opts CopyOptions) error {
	if err := src.selectBase(srcPath, opts.CopyDirContents); err != nil {
		return err
	}
	root := sourceEntry{
		Rel:      "",
		ViewPath: src.baseView,
		RealPath: src.baseReal,
		Info:     src.baseInfo,
	}

	destPath = cleanContainerPath(destPath)
	destInfo, destExists, err := c.dest.statView(destPath)
	if err != nil {
		return err
	}
	if opts.CopyDirContents && root.Info.IsDir() {
		if err := c.dest.removeForReplace(destPath, root.Info, opts); err != nil {
			return err
		}
		if _, _, err := c.dest.ensureDir(destPath, &root, opts, false); err != nil {
			return err
		}
		entries, err := src.readDir("")
		if err != nil {
			return err
		}
		for _, ent := range entries {
			if err := c.copyEntry(ctx, src, matcher, ent, filepath.Join(destPath, filepath.Base(ent.Rel)), opts, matchState{}, nil); err != nil {
				return err
			}
		}
		return nil
	}

	if !root.Info.IsDir() {
		if opts.DestPathHintIsDir {
			if _, _, err := c.dest.ensureDir(destPath, nil, CopyOptions{Chown: opts.Chown}, false); err != nil {
				return err
			}
			destPath = filepath.Join(destPath, filepath.Base(cleanContainerPath(srcPath)))
		} else if destExists && destInfo.IsDir() {
			destPath = filepath.Join(destPath, filepath.Base(cleanContainerPath(srcPath)))
		}
	} else if destExists && destInfo.IsDir() {
		destPath = filepath.Join(destPath, filepath.Base(src.baseView))
	}
	return c.copyEntry(ctx, src, matcher, root, destPath, opts, matchState{}, nil)
}

func (c *Copier) copyFile(ctx context.Context, src *source, srcPath, destPath string, opts CopyOptions) error {
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	default:
	}

	if err := src.selectBase(srcPath, true); err != nil {
		return err
	}
	if src.baseInfo.IsDir() {
		return fmt.Errorf("source path %q is a directory", srcPath)
	}
	destPath = cleanContainerPath(destPath)
	if opts.DestPathHintIsDir {
		if _, _, err := c.dest.ensureDir(destPath, nil, CopyOptions{Chown: opts.Chown}, false); err != nil {
			return err
		}
		destPath = filepath.Join(destPath, filepath.Base(cleanContainerPath(srcPath)))
	} else if destInfo, exists, err := c.dest.statView(destPath); err != nil {
		return err
	} else if exists && destInfo.IsDir() {
		destPath = filepath.Join(destPath, filepath.Base(cleanContainerPath(srcPath)))
	}

	ent := sourceEntry{
		Rel:      "",
		ViewPath: src.baseView,
		RealPath: src.baseReal,
		Info:     src.baseInfo,
	}
	return c.copyNode(ent, destPath, opts)
}

func (c *Copier) copyEntry(
	ctx context.Context,
	src *source,
	matcher *matcher,
	ent sourceEntry,
	destPath string,
	opts CopyOptions,
	parentState matchState,
	pending []pendingDir,
) error {
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	default:
	}

	include, state, err := matcher.includePath(ent.Rel, ent.ViewPath, ent.Info, parentState)
	if err != nil {
		return err
	}

	if ent.Info == nil {
		if !include {
			return nil
		}
		if ent.StatErr != nil {
			return fmt.Errorf("failed to stat source path %s: %w", ent.ViewPath, ent.StatErr)
		}
		return fmt.Errorf("source path %q missing file info", ent.ViewPath)
	}

	if ent.Info.IsDir() {
		childPending := pending
		var realDirPath string
		if include {
			if err := c.ensurePending(pending, opts); err != nil {
				return err
			}
			if err := c.dest.removeForReplace(destPath, ent.Info, opts); err != nil {
				return err
			}
			rel, _, err := c.dest.ensureDir(destPath, &ent, opts, true)
			if err != nil {
				return err
			}
			realDirPath = filepath.Join(c.dest.writeRoot, rel)
		} else {
			childPending = append(childPending, pendingDir{entry: ent, destPath: destPath})
		}

		if !matcher.shouldDescend(ent.Rel) {
			if include {
				return c.dest.applyMetadataPath(realDirPath, &ent, opts)
			}
			return nil
		}

		children, err := src.readDir(ent.Rel)
		if err != nil {
			return err
		}
		for _, child := range children {
			childDest := filepath.Join(destPath, filepath.Base(child.Rel))
			if err := c.copyEntry(ctx, src, matcher, child, childDest, opts, state, childPending); err != nil {
				return err
			}
		}
		if include {
			return c.dest.applyMetadataPath(realDirPath, &ent, opts)
		}
		return nil
	}

	if !include {
		return nil
	}
	if err := c.ensurePending(pending, opts); err != nil {
		return err
	}
	return c.copyNode(ent, destPath, opts)
}

func (c *Copier) ensurePending(pending []pendingDir, opts CopyOptions) error {
	for _, dir := range pending {
		if _, _, err := c.dest.ensureDir(dir.destPath, &dir.entry, opts, true); err != nil {
			return err
		}
	}
	return nil
}

func (c *Copier) copyNode(ent sourceEntry, destPath string, opts CopyOptions) error {
	if err := c.dest.removeForReplace(destPath, ent.Info, opts); err != nil {
		return err
	}
	realPath, err := c.dest.realPath(destPath)
	if err != nil {
		return err
	}

	mode := ent.Info.Mode()
	switch {
	case mode.Type() == 0:
		return c.copyRegular(ent, realPath, opts)
	case mode&os.ModeSymlink != 0:
		target, err := os.Readlink(ent.RealPath)
		if err != nil {
			return err
		}
		if err := os.RemoveAll(realPath); err != nil {
			return err
		}
		if err := os.Symlink(target, realPath); err != nil {
			return err
		}
	case mode&os.ModeDevice != 0, mode&os.ModeNamedPipe != 0, mode&os.ModeSocket != 0:
		if err := os.RemoveAll(realPath); err != nil {
			return err
		}
		if err := mknod(realPath, ent.Info); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported source file type %s at %q", mode.Type(), ent.ViewPath)
	}
	return c.dest.applyMetadataPath(realPath, &ent, opts)
}

func (c *Copier) copyRegular(ent sourceEntry, realPath string, opts CopyOptions) error {
	st, ok := ent.Info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("unexpected stat type %T", ent.Info.Sys())
	}
	var ino inode
	if !opts.DisableHardlinks {
		ino = statInode(st)
		if linkSrc, ok := c.dest.sourceLinks[ino]; ok {
			if err := os.RemoveAll(realPath); err != nil {
				return err
			}
			if err := os.Link(linkSrc, realPath); err != nil && !isHardlinkFallback(err) {
				return err
			} else if err == nil {
				return c.dest.applyMetadataPath(realPath, &ent, opts)
			}
		}
	}

	if !opts.DisableHardlinks && !opts.DisableSourceHardlinks && opts.Chown == nil && opts.Mode == nil {
		if err := os.RemoveAll(realPath); err != nil {
			return err
		}
		if err := os.Link(ent.RealPath, realPath); err == nil {
			c.dest.sourceLinks[ino] = realPath
			c.dest.crossLinks[ino] = struct{}{}
			return nil
		} else if !isHardlinkFallback(err) {
			return err
		}
	}

	if err := copyFileContent(realPath, ent.RealPath); err != nil {
		return err
	}
	if !opts.DisableHardlinks {
		c.dest.sourceLinks[ino] = realPath
	}
	return c.dest.applyMetadataPath(realPath, &ent, opts)
}

func isHardlinkFallback(err error) bool {
	return err != nil && (os.IsExist(err) || err == unix.EXDEV || err == unix.EMLINK || err == syscall.EXDEV || err == syscall.EMLINK)
}

func copyFileContent(dstPath, srcPath string) error {
	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer src.Close()

	dst, err := os.Create(dstPath)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(dst, src)
	closeErr := dst.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func mknod(dstPath string, info os.FileInfo) error {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("unexpected stat type %T", info.Sys())
	}
	return unix.Mknod(dstPath, st.Mode, int(st.Rdev))
}
