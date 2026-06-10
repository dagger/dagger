//go:build linux

package layercopy

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/containerd/containerd/v2/core/snapshots"
	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
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

func (c *Copier) Copy(ctx context.Context, src Mount, srcPath, destPath string, opts CopyOptions) (rerr error) {
	stats := opts.Stats
	if stats == nil {
		stats = &CopyStats{}
		opts.Stats = stats
	}
	ctx, span := Tracer(ctx).Start(ctx, "layercopy.copy", trace.WithAttributes(
		attribute.String("layercopy.src_path", srcPath),
		attribute.String("layercopy.dest_path", destPath),
		attribute.Bool("layercopy.copy_dir_contents", opts.CopyDirContents),
		attribute.Bool("layercopy.replace_existing", opts.ReplaceExisting),
		attribute.Bool("layercopy.disable_hardlinks", opts.DisableHardlinks),
		attribute.Bool("layercopy.disable_source_hardlinks", opts.DisableSourceHardlinks),
		attribute.Int("layercopy.only_count", len(opts.Filter.Only)),
		attribute.Int("layercopy.include_pattern_count", len(opts.Filter.Include)),
		attribute.Int("layercopy.exclude_pattern_count", len(opts.Filter.Exclude)),
	))
	defer func() {
		span.SetAttributes(copyStatsAttributes(stats)...)
		telemetry.EndWithCause(span, &rerr)
	}()

	var s *source
	_, setupSourceSpan := Tracer(ctx).Start(ctx, "layercopy.setup-source")
	s, err := newSource(src)
	setupSourceErr := err
	telemetry.EndWithCause(setupSourceSpan, &setupSourceErr)
	if err != nil {
		return err
	}
	_, setupMatcherSpan := Tracer(ctx).Start(ctx, "layercopy.setup-matcher")
	m, err := newMatcher(s.root, opts.Filter)
	setupMatcherErr := err
	telemetry.EndWithCause(setupMatcherSpan, &setupMatcherErr)
	if err != nil {
		return err
	}
	_, copyTreeSpan := Tracer(ctx).Start(ctx, "layercopy.walk-and-copy")
	err = c.copy(ctx, s, m, srcPath, destPath, opts)
	copyTreeErr := err
	telemetry.EndWithCause(copyTreeSpan, &copyTreeErr)
	return err
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
		start := time.Now()
		entries, err := src.readDir("")
		if opts.Stats != nil {
			opts.Stats.ReadDirCalls++
			opts.Stats.ReadDirDuration += time.Since(start)
		}
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

	if opts.Stats != nil {
		opts.Stats.EntriesVisited++
	}
	include, state, err := matcher.includePath(ent.Rel, ent.ViewPath, ent.Info, parentState)
	if err != nil {
		return err
	}
	if opts.Stats != nil {
		switch {
		case ent.Info.IsDir():
			opts.Stats.Dirs++
		case ent.Info.Mode().Type() == 0:
			opts.Stats.RegularFiles++
		case ent.Info.Mode()&os.ModeSymlink != 0:
			opts.Stats.Symlinks++
		case ent.Info.Mode()&(os.ModeDevice|os.ModeNamedPipe|os.ModeSocket) != 0:
			opts.Stats.SpecialFiles++
		}
		if include {
			opts.Stats.Included++
		} else {
			opts.Stats.Skipped++
		}
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

		start := time.Now()
		children, err := src.readDir(ent.Rel)
		if opts.Stats != nil {
			opts.Stats.ReadDirCalls++
			opts.Stats.ReadDirDuration += time.Since(start)
		}
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

	start := time.Now()
	written, err := copyFileContent(realPath, ent.RealPath)
	if opts.Stats != nil {
		opts.Stats.ContentCopyCalls++
		opts.Stats.ContentCopyDuration += time.Since(start)
		opts.Stats.BytesCopied += written
	}
	if err != nil {
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

func copyFileContent(dstPath, srcPath string) (int64, error) {
	src, err := os.Open(srcPath)
	if err != nil {
		return 0, err
	}
	defer src.Close()

	dst, err := os.Create(dstPath)
	if err != nil {
		return 0, err
	}
	written, copyErr := io.Copy(dst, src)
	closeErr := dst.Close()
	if copyErr != nil {
		return written, copyErr
	}
	return written, closeErr
}

func copyStatsAttributes(stats *CopyStats) []attribute.KeyValue {
	if stats == nil {
		return nil
	}
	return []attribute.KeyValue{
		attribute.Int64("layercopy.entries_visited", stats.EntriesVisited),
		attribute.Int64("layercopy.included", stats.Included),
		attribute.Int64("layercopy.skipped", stats.Skipped),
		attribute.Int64("layercopy.dirs", stats.Dirs),
		attribute.Int64("layercopy.regular_files", stats.RegularFiles),
		attribute.Int64("layercopy.symlinks", stats.Symlinks),
		attribute.Int64("layercopy.special_files", stats.SpecialFiles),
		attribute.Int64("layercopy.read_dir_calls", stats.ReadDirCalls),
		attribute.Int64("layercopy.read_dir_duration_ms", stats.ReadDirDuration.Milliseconds()),
		attribute.Int64("layercopy.ensure_dir_calls", stats.EnsureDirCalls),
		attribute.Int64("layercopy.ensure_dir_duration_ms", stats.EnsureDirDuration.Milliseconds()),
		attribute.Int64("layercopy.created_dirs", stats.CreatedDirs),
		attribute.Int64("layercopy.materialized_dirs", stats.MaterializedDirs),
		attribute.Int64("layercopy.remove_calls", stats.RemoveCalls),
		attribute.Int64("layercopy.remove_duration_ms", stats.RemoveDuration.Milliseconds()),
		attribute.Int64("layercopy.content_copy_calls", stats.ContentCopyCalls),
		attribute.Int64("layercopy.content_copy_duration_ms", stats.ContentCopyDuration.Milliseconds()),
		attribute.Int64("layercopy.bytes_copied", stats.BytesCopied),
		attribute.Int64("layercopy.metadata_calls", stats.MetadataCalls),
		attribute.Int64("layercopy.metadata_duration_ms", stats.MetadataDuration.Milliseconds()),
		attribute.Int64("layercopy.xattr_list_calls", stats.XAttrListCalls),
		attribute.Int64("layercopy.xattr_get_calls", stats.XAttrGetCalls),
		attribute.Int64("layercopy.xattr_set_calls", stats.XAttrSetCalls),
		attribute.Int64("layercopy.xattr_duration_ms", stats.XAttrDuration.Milliseconds()),
	}
}

func mknod(dstPath string, info os.FileInfo) error {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("unexpected stat type %T", info.Sys())
	}
	return unix.Mknod(dstPath, st.Mode, int(st.Rdev))
}
