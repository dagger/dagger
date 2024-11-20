package local

import (
	"context"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	gofs "io/fs"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/containerd/continuity/sysx"
	"github.com/moby/buildkit/cache"
	"github.com/moby/buildkit/cache/contenthash"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/snapshot"
	"github.com/moby/buildkit/util/bklog"
	"github.com/moby/buildkit/util/flightcontrol"
	digest "github.com/opencontainers/go-digest"
	"github.com/tonistiigi/fsutil"
	fscopy "github.com/tonistiigi/fsutil/copy"
	"github.com/tonistiigi/fsutil/types"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sys/unix"
)

type localFSSharedState struct {
	rootPath string
	g        flightcontrol.CachedGroup[*ChangeWithStat]
}

type ChangeWithStat struct {
	kind ChangeKind
	stat *HashedStatInfo
}

func (c *ChangeWithStat) IsEqual(other *ChangeWithStat) bool {
	// TODO: MORE ROBUST
	if c.kind != other.kind {
		return false
	}
	if c.stat == nil && other.stat == nil {
		return true
	}
	if c.stat == nil && other.stat != nil {
		return false
	}
	if c.stat != nil && other.stat == nil {
		return false
	}
	if c.stat.Mode() != other.stat.Mode() {
		return false
	}
	return true
}

type ErrConflict struct {
	Path string
	Old  *ChangeWithStat
	New  *ChangeWithStat
}

func (e *ErrConflict) Error() string {
	// TODO: MAKE HUMAN READABLE
	return fmt.Sprintf("conflict at %s: %+v vs. %+v", e.Path, e.Old, e.New)
}

type localFS struct {
	*localFSSharedState

	subdir string

	filterFS fsutil.FS
	includes []string
	excludes []string
}

func NewLocalFS(sharedState *localFSSharedState, subdir string, includes, excludes []string) (*localFS, error) {
	baseFS, err := fsutil.NewFS(filepath.Join(sharedState.rootPath, subdir))
	if err != nil {
		return nil, fmt.Errorf("failed to create base fs: %w", err)
	}
	// TODO: slight optimization by skipping this FS if no include/exclude?
	filterFS, err := fsutil.NewFilterFS(baseFS, &fsutil.FilterOpt{
		IncludePatterns: includes,
		ExcludePatterns: excludes,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create filter fs: %w", err)
	}

	return &localFS{
		localFSSharedState: sharedState,
		subdir:             subdir,
		filterFS:           filterFS,
		includes:           includes,
		excludes:           excludes,
	}, nil
}

func (local *localFS) Sync(
	ctx context.Context,
	remote ReadFS,
	cacheManager cache.Accessor,
	session session.Group,
	// TODO: ugly af
	forParents bool,
) (_ cache.ImmutableRef, rerr error) {
	var newCopyRef cache.MutableRef
	var cacheCtx contenthash.CacheContext
	if !forParents {
		var err error
		newCopyRef, err = cacheManager.New(ctx, nil, nil) // TODO: any opts? description? don't forget to set Retain once known
		if err != nil {
			return nil, fmt.Errorf("failed to create new copy ref: %w", err)
		}
		defer func() {
			ctx := context.WithoutCancel(ctx)
			if newCopyRef != nil {
				if err := newCopyRef.Release(ctx); err != nil {
					rerr = errors.Join(rerr, fmt.Errorf("failed to release copy ref: %w", err))
				}
			}
		}()

		cacheCtx, err = contenthash.GetCacheContext(ctx, newCopyRef)
		if err != nil {
			return nil, fmt.Errorf("failed to get cache context: %w", err)
		}
	}

	eg, egCtx := errgroup.WithContext(ctx)

	eg.Go(func() error {
		return doubleWalkDiff(egCtx, local, remote, func(kind ChangeKind, path string, lowerStat, upperStat *types.Stat) error {
			/*
				// TODO:
				// TODO:
				// TODO:
				// TODO:
				bklog.G(egCtx).Debugf("DIFF %s %s (%s) (%s)", kind, path, local.toRootPath(path), local.toFullPath(path))
			*/

			var appliedChange *ChangeWithStat
			var err error
			switch kind {
			case ChangeKindAdd, ChangeKindModify:
				switch {
				case upperStat.IsDir():
					// TODO: handle parent dir mod times
					appliedChange, err = local.Mkdir(egCtx, path, upperStat)

				case upperStat.Mode&uint32(os.ModeDevice) != 0 || upperStat.Mode&uint32(os.ModeNamedPipe) != 0:
					// TODO:

				case upperStat.Mode&uint32(os.ModeSymlink) != 0:
					appliedChange, err = local.Symlink(egCtx, path, upperStat)

				case upperStat.Linkname != "":
					appliedChange, err = local.Hardlink(egCtx, path, upperStat)

				default:
					eg.Go(func() error {
						// TODO: DOUBLE CHECK IF YOU NEED TO COPY STAT OBJS SINCE THIS IS ASYNC
						appliedChange, err := local.WriteFile(egCtx, path, upperStat, remote)
						if err != nil {
							return err
						}
						if cacheCtx != nil {
							// TODO:
							// TODO:
							// TODO:
							// bklog.G(ctx).Debugf("CACHECTX HANDLE CHANGE FILE %s %s %+v", appliedChange.kind, path, appliedChange.stat)

							if err := cacheCtx.HandleChange(appliedChange.kind, path, appliedChange.stat, nil); err != nil {
								return fmt.Errorf("failed to handle change in content hasher: %w", err)
							}
						}

						return nil
					})
					return nil
				}

			case ChangeKindDelete:
				// TODO: do we even need to apply this to the cacheCtx? May actually cause an error, consider skipping that
				appliedChange, err = local.RemoveAll(egCtx, path)

			case ChangeKindNone:
				appliedChange, err = local.getPreviousChange(path)

			default:
				return fmt.Errorf("unsupported change kind: %s", kind)
			}
			if err != nil {
				return err
			}
			if cacheCtx != nil {
				// TODO:
				// TODO:
				// TODO:
				// bklog.G(ctx).Debugf("CACHECTX HANDLE CHANGE %s %s %+v", appliedChange.kind, path, appliedChange.stat)

				if err := cacheCtx.HandleChange(appliedChange.kind, path, appliedChange.stat, nil); err != nil {
					return fmt.Errorf("failed to handle change in content hasher: %w", err)
				}
			}

			return nil
		})
	})
	if err := eg.Wait(); err != nil {
		return nil, err
	}

	if forParents {
		return nil, nil
	}

	// TODO: should probably provide ref impl that just errors if mount is attempted; should never be needed
	dgst, err := cacheCtx.Checksum(ctx, newCopyRef, "/", contenthash.ChecksumOpts{}, session)
	if err != nil {
		return nil, fmt.Errorf("failed to checksum: %w", err)
	}

	sis, err := SearchContentHash(ctx, cacheManager, dgst)
	if err != nil {
		return nil, fmt.Errorf("failed to search content hash: %w", err)
	}
	for _, si := range sis {
		finalRef, err := cacheManager.Get(ctx, si.ID(), nil)
		if err == nil {
			// TODO:
			// TODO:
			// TODO:
			bklog.G(ctx).Debugf("REUSING COPY REF: %s", finalRef.ID())
			return finalRef, nil
		} else {
			bklog.G(ctx).Debugf("failed to get cache ref: %v", err)
		}
	}

	copyRefMntable, err := newCopyRef.Mount(ctx, false, session)
	if err != nil {
		return nil, fmt.Errorf("failed to get mountable: %w", err)
	}
	copyRefMnter := snapshot.LocalMounter(copyRefMntable)
	copyRefMntPath, err := copyRefMnter.Mount()
	if err != nil {
		return nil, fmt.Errorf("failed to mount: %w", err)
	}
	defer func() {
		if copyRefMnter != nil {
			if err := copyRefMnter.Unmount(); err != nil {
				rerr = errors.Join(rerr, fmt.Errorf("failed to unmount: %w", err))
			}
		}
	}()

	copyOpts := []fscopy.Opt{
		func(ci *fscopy.CopyInfo) {
			ci.IncludePatterns = local.includes
			ci.ExcludePatterns = local.excludes
			ci.CopyDirContents = true
		},
		fscopy.WithXAttrErrorHandler(func(dst, src, key string, err error) error {
			bklog.G(ctx).Debugf("xattr error during local import copy: %v", err)
			return nil
		}),
	}

	/* TODO: equivalent of this
	defer func() {
		var osErr *os.PathError
		if errors.As(err, &osErr) {
			// remove system root from error path if present
			osErr.Path = strings.TrimPrefix(osErr.Path, src)
			osErr.Path = strings.TrimPrefix(osErr.Path, dest)
		}
	}()
	*/

	if err := fscopy.Copy(ctx,
		local.rootPath, local.subdir,
		copyRefMntPath, "/",
		copyOpts...,
	); err != nil {
		return nil, fmt.Errorf("failed to copy %q: %w", local.subdir, err)
	}

	if err := copyRefMnter.Unmount(); err != nil {
		copyRefMnter = nil
		return nil, fmt.Errorf("failed to unmount: %w", err)
	}
	copyRefMnter = nil

	finalRef, err := newCopyRef.Commit(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to commit: %w", err)
	}
	defer func() {
		if rerr != nil {
			if finalRef != nil {
				ctx := context.WithoutCancel(ctx)
				if err := finalRef.Release(ctx); err != nil {
					rerr = errors.Join(rerr, fmt.Errorf("failed to release: %w", err))
				}
			}
		}
	}()

	if err := finalRef.Finalize(ctx); err != nil {
		return nil, fmt.Errorf("failed to finalize: %w", err)
	}
	if err := contenthash.SetCacheContext(ctx, finalRef, cacheCtx); err != nil {
		return nil, fmt.Errorf("failed to set cache context: %w", err)
	}
	if err := (CacheRefMetadata{finalRef}).SetContentHashKey(dgst); err != nil {
		return nil, fmt.Errorf("failed to set content hash key: %w", err)
	}
	if err := finalRef.SetCachePolicyRetain(); err != nil {
		return nil, fmt.Errorf("failed to set cache policy: %w", err)
	}

	// NOTE: this MUST be after setting cache policy retain or bk cache manager decides to
	// remove finalRef...
	if err := newCopyRef.Release(ctx); err != nil {
		newCopyRef = nil
		return nil, fmt.Errorf("failed to release: %w", err)
	}
	newCopyRef = nil

	return finalRef, nil
}

// the full absolute path on the local filesystem
func (local *localFS) toFullPath(path string) string {
	// TODO: use fs.RootPath to be extra safe?
	return filepath.Join(local.rootPath, local.subdir, path)
}

// the absolute path under local.rootPath
func (local *localFS) toRootPath(path string) string {
	// TODO: use fs.RootPath to be extra safe?
	return filepath.Join(local.subdir, path)
}

func (local *localFS) getPreviousChange(path string) (*ChangeWithStat, error) {
	// TODO: optimize with separate non-cached flightcontrol group? LRU type cache would maybe be even better
	fullPath := local.toFullPath(path)

	// TODO: there's some util somewhere that would go right to fsStat, right?
	stat, err := os.Lstat(fullPath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat path: %w", err)
	}
	fsStat, err := fsStatFromOs(fullPath, stat)
	if err != nil {
		return nil, fmt.Errorf("failed to convert stat: %w", err)
	}

	if stat.Mode().IsRegular() {
		dgstBytes, err := sysx.Getxattr(fullPath, "user.daggerContentHash")
		if err != nil {
			return nil, fmt.Errorf("failed to get content hash xattr: %w", err)
		}
		hashStat := &HashedStatInfo{StatInfo{fsStat}, digest.Digest(dgstBytes)}
		return &ChangeWithStat{kind: ChangeKindAdd, stat: hashStat}, nil
	}

	// TODO: can avoid this on directory types too since they allow xattrs

	h, err := contenthash.NewFromStat(fsStat)
	if err != nil {
		return nil, fmt.Errorf("failed to create content hash: %w", err)
	}

	hashStat := &HashedStatInfo{StatInfo{fsStat}, digest.NewDigest(digest.SHA256, h)}
	return &ChangeWithStat{kind: ChangeKindAdd, stat: hashStat}, nil
}

func (local *localFS) mutate(
	ctx context.Context,
	path string,
	upperStat *types.Stat,
	fn func(ctx context.Context, fullPath string, lowerStat fs.FileInfo, h hash.Hash) error,
) (*ChangeWithStat, error) {
	rootPath := local.toRootPath(path)
	fullPath := local.toFullPath(path)
	return local.g.Do(ctx, rootPath, func(ctx context.Context) (*ChangeWithStat, error) {
		var changeKind ChangeKind
		switch {
		case upperStat != nil: // add or modify
			changeKind = ChangeKindAdd // TODO: explain, or make better
			lowerStat, err := os.Lstat(fullPath)
			if err != nil && !os.IsNotExist(err) {
				return nil, fmt.Errorf("failed to stat existing path: %w", err)
			}

			h, err := contenthash.NewFromStat(upperStat)
			if err != nil {
				return nil, fmt.Errorf("failed to create content hash: %w", err)
			}
			err = fn(ctx, fullPath, lowerStat, h)
			if err != nil {
				return nil, err
			}
			hashStat := &HashedStatInfo{StatInfo{upperStat}, digest.NewDigest(digest.SHA256, h)}
			return &ChangeWithStat{kind: changeKind, stat: hashStat}, nil

		default: // delete
			changeKind = ChangeKindDelete
			err := fn(ctx, fullPath, nil, nil)
			if err != nil {
				return nil, err
			}

			return &ChangeWithStat{kind: changeKind, stat: nil}, nil
		}
	})
}

func (local *localFS) RemoveAll(ctx context.Context, path string) (*ChangeWithStat, error) {
	appliedChange, err := local.mutate(ctx, path, nil, func(ctx context.Context, fullPath string, _ fs.FileInfo, _ hash.Hash) error {
		return os.RemoveAll(fullPath)
	})
	if err != nil {
		return nil, err
	}
	expectedChange := &ChangeWithStat{kind: ChangeKindDelete, stat: nil}
	if !appliedChange.IsEqual(expectedChange) {
		return nil, &ErrConflict{Path: path, Old: appliedChange, New: expectedChange}
	}
	return appliedChange, nil
}

func (local *localFS) Mkdir(ctx context.Context, path string, upperStat *types.Stat) (*ChangeWithStat, error) {
	appliedChange, err := local.mutate(ctx, path, upperStat, func(ctx context.Context, fullPath string, lowerStat fs.FileInfo, _ hash.Hash) error {
		isNewDir := lowerStat == nil
		replacesNonDir := lowerStat != nil && !lowerStat.IsDir()

		if replacesNonDir {
			if err := os.Remove(fullPath); err != nil {
				return fmt.Errorf("failed to remove existing file: %w", err)
			}
		}

		if isNewDir || replacesNonDir {
			if err := os.Mkdir(fullPath, os.FileMode(upperStat.Mode)&os.ModePerm); err != nil {
				return fmt.Errorf("failed to create directory: %w", err)
			}
		}

		if err := rewriteMetadata(fullPath, upperStat); err != nil {
			return fmt.Errorf("failed to rewrite directory metadata: %w", err)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	// TODO: here and elsewhere, making a HashedStatInfo with no digest is ugly
	expectedChange := &ChangeWithStat{kind: ChangeKindAdd, stat: &HashedStatInfo{StatInfo: StatInfo{upperStat}}}
	if !appliedChange.IsEqual(expectedChange) {
		return nil, &ErrConflict{Path: path, Old: appliedChange, New: expectedChange}
	}
	return appliedChange, nil
}

func (local *localFS) Symlink(ctx context.Context, path string, upperStat *types.Stat) (*ChangeWithStat, error) {
	appliedChange, err := local.mutate(ctx, path, upperStat, func(ctx context.Context, fullPath string, lowerStat fs.FileInfo, _ hash.Hash) error {
		isNewSymlink := lowerStat == nil
		replacesNonSymlink := lowerStat != nil && lowerStat.Mode()&fs.ModeSymlink == 0

		if replacesNonSymlink {
			if err := os.RemoveAll(fullPath); err != nil {
				return fmt.Errorf("failed to remove existing file: %w", err)
			}
		}

		if isNewSymlink || replacesNonSymlink {
			if err := os.Symlink(upperStat.Linkname, fullPath); err != nil {
				return fmt.Errorf("failed to create symlink: %w", err)
			}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}
	expectedChange := &ChangeWithStat{kind: ChangeKindAdd, stat: &HashedStatInfo{StatInfo: StatInfo{upperStat}}}
	if !appliedChange.IsEqual(expectedChange) {
		return nil, &ErrConflict{Path: path, Old: appliedChange, New: expectedChange}
	}
	return appliedChange, nil
}

func (local *localFS) Hardlink(ctx context.Context, path string, upperStat *types.Stat) (*ChangeWithStat, error) {
	appliedChange, err := local.mutate(ctx, path, upperStat, func(ctx context.Context, fullPath string, lowerStat fs.FileInfo, _ hash.Hash) error {
		// TODO: this is incomplete, should remove in all cases unless it's already the same hardlink (due to concurrency, but that's probably better fixed more generally elsewhere)
		isNewLink := lowerStat == nil

		if isNewLink {
			if err := os.RemoveAll(fullPath); err != nil {
				return fmt.Errorf("failed to remove existing file: %w", err)
			}
		}

		// TODO: worth a double check on the path joining logic here
		// TODO: at least worst case it can't cross the mount
		if err := os.Link(local.toFullPath(upperStat.Linkname), fullPath); err != nil {
			return fmt.Errorf("failed to create symlink: %w", err)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}
	expectedChange := &ChangeWithStat{kind: ChangeKindAdd, stat: &HashedStatInfo{StatInfo: StatInfo{upperStat}}}
	if !appliedChange.IsEqual(expectedChange) {
		return nil, &ErrConflict{Path: path, Old: appliedChange, New: expectedChange}
	}
	return appliedChange, nil
}

func (local *localFS) WriteFile(ctx context.Context, path string, upperStat *types.Stat, upperFS ReadFS) (*ChangeWithStat, error) {
	appliedChange, err := local.mutate(ctx, path, upperStat, func(ctx context.Context, fullPath string, lowerStat fs.FileInfo, h hash.Hash) error {
		reader, err := upperFS.ReadFile(ctx, path)
		if err != nil {
			return fmt.Errorf("failed to read file: %w", err)
		}
		defer reader.Close()

		if lowerStat != nil {
			if err := os.RemoveAll(fullPath); err != nil {
				return fmt.Errorf("failed to remove existing file: %w", err)
			}
		}

		f, err := os.OpenFile(fullPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(upperStat.Mode)&os.ModePerm)
		if err != nil {
			return err
		}
		defer f.Close()

		if _, err := io.Copy(io.MultiWriter(f, h), reader); err != nil {
			return fmt.Errorf("failed to copy contents: %w", err)
		}
		if err := f.Close(); err != nil {
			return fmt.Errorf("failed to close file: %w", err)
		}

		if err := rewriteMetadata(fullPath, upperStat); err != nil {
			return fmt.Errorf("failed to rewrite file metadata: %w", err)
		}

		dgst := digest.NewDigest(digest.SHA256, h)
		// TODO: dedupe; constify
		if err := sysx.Setxattr(fullPath, "user.daggerContentHash", []byte(dgst.String()), 0); err != nil {
			return fmt.Errorf("failed to set content hash xattr: %w", err)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}
	expectedChange := &ChangeWithStat{kind: ChangeKindAdd, stat: &HashedStatInfo{StatInfo: StatInfo{upperStat}}}
	if !appliedChange.IsEqual(expectedChange) {
		return nil, &ErrConflict{Path: path, Old: appliedChange, New: expectedChange}
	}
	return appliedChange, nil
}

func (local *localFS) Walk(ctx context.Context, path string, walkFn gofs.WalkDirFunc) error {
	return local.filterFS.Walk(ctx, path, walkFn)
}

type StatInfo struct {
	*types.Stat
}

func (s *StatInfo) Name() string {
	return filepath.Base(s.Stat.Path)
}

func (s *StatInfo) Size() int64 {
	return s.Stat.Size_
}

func (s *StatInfo) Mode() os.FileMode {
	return os.FileMode(s.Stat.Mode)
}

func (s *StatInfo) ModTime() time.Time {
	return time.Unix(s.Stat.ModTime/1e9, s.Stat.ModTime%1e9)
}

func (s *StatInfo) IsDir() bool {
	return s.Mode().IsDir()
}

func (s *StatInfo) Sys() interface{} {
	return s.Stat
}

func (s *StatInfo) Type() gofs.FileMode {
	return gofs.FileMode(s.Stat.Mode)
}

func (s *StatInfo) Info() (gofs.FileInfo, error) {
	return s, nil
}

func fsStatFromOs(fullPath string, fi os.FileInfo) (*types.Stat, error) {
	var link string
	if fi.Mode()&os.ModeSymlink != 0 {
		var err error
		link, err = os.Readlink(fullPath)
		if err != nil {
			return nil, err
		}
	}

	stat := &types.Stat{
		Mode:     uint32(fi.Mode()),
		Size_:    fi.Size(),
		ModTime:  fi.ModTime().UnixNano(),
		Linkname: link,
	}

	if fi.Mode()&os.ModeSymlink != 0 {
		stat.Mode = stat.Mode | 0777
	}

	if err := setUnixOpt(fullPath, fi, stat); err != nil {
		return nil, err
	}

	return stat, nil
}

func setUnixOpt(path string, fi os.FileInfo, stat *types.Stat) error {
	s := fi.Sys().(*syscall.Stat_t)

	stat.Uid = s.Uid
	stat.Gid = s.Gid

	if !fi.IsDir() {
		if s.Mode&syscall.S_IFLNK == 0 && (s.Mode&syscall.S_IFBLK != 0 ||
			s.Mode&syscall.S_IFCHR != 0) {
			stat.Devmajor = int64(unix.Major(uint64(s.Rdev)))
			stat.Devminor = int64(unix.Minor(uint64(s.Rdev)))
		}
	}

	attrs, err := sysx.LListxattr(path)
	if err != nil {
		return err
	}
	if len(attrs) > 0 {
		stat.Xattrs = map[string][]byte{}
		for _, attr := range attrs {
			v, err := sysx.LGetxattr(path, attr)
			if err == nil {
				stat.Xattrs[attr] = v
			}
		}
	}
	return nil
}

type HashedStatInfo struct {
	StatInfo
	dgst digest.Digest
}

func (s *HashedStatInfo) Digest() digest.Digest {
	return s.dgst
}

func rewriteMetadata(p string, upperStat *types.Stat) error {
	for key, value := range upperStat.Xattrs {
		sysx.Setxattr(p, key, value, 0)
	}

	if err := os.Lchown(p, int(upperStat.Uid), int(upperStat.Gid)); err != nil {
		return fmt.Errorf("failed to change owner: %w", err)
	}

	if os.FileMode(upperStat.Mode)&os.ModeSymlink == 0 {
		if err := os.Chmod(p, os.FileMode(upperStat.Mode)); err != nil {
			return fmt.Errorf("failed to change mode: %w", err)
		}
	}

	if err := chtimes(p, upperStat.ModTime); err != nil {
		return fmt.Errorf("failed to change mod time: %w", err)
	}

	return nil
}

func chtimes(path string, un int64) error {
	var utimes [2]unix.Timespec
	utimes[0] = unix.NsecToTimespec(un)
	utimes[1] = utimes[0]

	if err := unix.UtimesNanoAt(unix.AT_FDCWD, path, utimes[0:], unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return fmt.Errorf("failed to call utimes: %w", err)
	}

	return nil
}
