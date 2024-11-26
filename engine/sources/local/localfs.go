package local

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/containerd/continuity/sysx"
	"github.com/dagger/dagger/engine/contenthash"
	"github.com/moby/buildkit/cache"
	bkcontenthash "github.com/moby/buildkit/cache/contenthash"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/snapshot"
	"github.com/moby/buildkit/util/bklog"
	digest "github.com/opencontainers/go-digest"
	"github.com/tonistiigi/fsutil"
	fscopy "github.com/tonistiigi/fsutil/copy"
	"github.com/tonistiigi/fsutil/types"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sys/unix"
)

const (
	hashXattrKey = "user.daggerContentHash"
)

type localFSSharedState struct {
	rootPath string
	g        SingleflightGroup[string, *ChangeWithStat]
}

type ChangeWithStat struct {
	kind ChangeKind
	stat *HashedStatInfo
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

// NOTE: This currently does not handle resetting parent dir modtimes to match the client. This matches
// upstream for now and avoids extra performance overhead + complication until a need for those modtimes
// matching the client's is proven.
func (local *localFS) Sync(
	ctx context.Context,
	remote ReadFS,
	cacheManager cache.Accessor,
	session session.Group,
	// TODO: ugly af
	forParents bool,
) (_ cache.ImmutableRef, rerr error) {
	var newCopyRef cache.MutableRef
	var cacheCtx bkcontenthash.CacheContext
	if !forParents {
		var err error
		newCopyRef, err = cacheManager.New(ctx, nil, nil)
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

		cacheCtx, err = bkcontenthash.GetCacheContext(ctx, newCopyRef)
		if err != nil {
			return nil, fmt.Errorf("failed to get cache context: %w", err)
		}
	}

	eg, egCtx := errgroup.WithContext(ctx)

	var cachedResults []*CachedResult[string, *ChangeWithStat]
	var cachedResultsMu sync.Mutex
	defer func() {
		for _, cachedResult := range cachedResults {
			cachedResult.Release()
		}
	}()

	type hardlinkChange struct {
		kind      ChangeKind
		path      string
		upperStat *types.Stat
	}
	var hardlinks []*hardlinkChange
	var hardlinkMu sync.Mutex

	doubleWalkDiff(egCtx, eg, local, remote, func(kind ChangeKind, path string, lowerStat, upperStat *types.Stat) error {
		switch kind {
		case ChangeKindAdd, ChangeKindModify:
			switch {
			case upperStat.IsDir():
				appliedChange, err := local.Mkdir(egCtx, kind, path, upperStat)
				if err != nil {
					return err
				}
				cachedResultsMu.Lock()
				cachedResults = append(cachedResults, appliedChange)
				cachedResultsMu.Unlock()
				if cacheCtx != nil {
					if err := cacheCtx.HandleChange(appliedChange.Result().kind, path, appliedChange.Result().stat, nil); err != nil {
						return fmt.Errorf("failed to handle change in content hasher: %w", err)
					}
				}

				return nil

			case upperStat.Mode&uint32(os.ModeDevice) != 0 || upperStat.Mode&uint32(os.ModeNamedPipe) != 0:
				// NOTE: not handling devices for now since they are extremely non-portable and dubious in terms
				// of real utility vs. enabling bizarre hacks
				return nil

			case upperStat.Mode&uint32(os.ModeSymlink) != 0:
				appliedChange, err := local.Symlink(egCtx, kind, path, upperStat)
				if err != nil {
					return err
				}
				cachedResultsMu.Lock()
				cachedResults = append(cachedResults, appliedChange)
				cachedResultsMu.Unlock()
				if cacheCtx != nil {
					if err := cacheCtx.HandleChange(appliedChange.Result().kind, path, appliedChange.Result().stat, nil); err != nil {
						return fmt.Errorf("failed to handle change in content hasher: %w", err)
					}
				}

				return nil

			case upperStat.Linkname != "":
				// delay hardlinks until after everything else so we know the source of the link exists
				hardlinkMu.Lock()
				hardlinks = append(hardlinks, &hardlinkChange{
					kind:      kind,
					path:      path,
					upperStat: upperStat,
				})
				hardlinkMu.Unlock()

				return nil

			default:
				eg.Go(func() error {
					appliedChange, err := local.WriteFile(egCtx, kind, path, upperStat, remote)
					if err != nil {
						return err
					}
					cachedResultsMu.Lock()
					cachedResults = append(cachedResults, appliedChange)
					cachedResultsMu.Unlock()
					if cacheCtx != nil {
						if err := cacheCtx.HandleChange(appliedChange.Result().kind, path, appliedChange.Result().stat, nil); err != nil {
							return fmt.Errorf("failed to handle change in content hasher: %w", err)
						}
					}

					return nil
				})
				return nil
			}

		case ChangeKindDelete:
			appliedChange, err := local.RemoveAll(egCtx, path)
			if err != nil {
				return err
			}
			cachedResultsMu.Lock()
			cachedResults = append(cachedResults, appliedChange)
			cachedResultsMu.Unlock()
			// no need to apply removals to the cacheCtx
			return nil

		case ChangeKindNone:
			appliedChange, err := local.GetPreviousChange(egCtx, path, lowerStat)
			if err != nil {
				return err
			}
			cachedResultsMu.Lock()
			cachedResults = append(cachedResults, appliedChange)
			cachedResultsMu.Unlock()
			if cacheCtx != nil {
				if err := cacheCtx.HandleChange(appliedChange.Result().kind, path, appliedChange.Result().stat, nil); err != nil {
					return fmt.Errorf("failed to handle change in content hasher: %w", err)
				}
			}

			return nil

		default:
			return fmt.Errorf("unsupported change kind: %s", kind)
		}
	})

	if err := eg.Wait(); err != nil {
		return nil, err
	}

	for _, hardlink := range hardlinks {
		appliedChange, err := local.Hardlink(ctx, hardlink.kind, hardlink.path, hardlink.upperStat)
		if err != nil {
			return nil, err
		}
		cachedResultsMu.Lock()
		cachedResults = append(cachedResults, appliedChange)
		cachedResultsMu.Unlock()
		if cacheCtx != nil {
			if err := cacheCtx.HandleChange(appliedChange.Result().kind, hardlink.path, appliedChange.Result().stat, nil); err != nil {
				return nil, fmt.Errorf("failed to handle change in content hasher: %w", err)
			}
		}
	}

	if forParents {
		return nil, nil
	}

	ctx, copySpan := newSpan(ctx, "copy")
	defer copySpan.End()

	dgst, err := cacheCtx.Checksum(ctx, newCopyRef, "/", bkcontenthash.ChecksumOpts{}, session)
	if err != nil {
		return nil, fmt.Errorf("failed to checksum: %w", err)
	}

	sis, err := contenthash.SearchContentHash(ctx, cacheManager, dgst)
	if err != nil {
		return nil, fmt.Errorf("failed to search content hash: %w", err)
	}
	for _, si := range sis {
		finalRef, err := cacheManager.Get(ctx, si.ID(), nil)
		if err == nil {
			bklog.G(ctx).Debugf("reusing copy ref %s", si.ID())
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

	// FIXME: when the ID of the ref given to SetCacheContext is different from the ID of the
	// ref the cacheCtx was created with, buildkit just stores it in a in-memory LRU that's
	// only hit by some code paths. This is probably a bug. To coerce it into actually storing
	// the cacheCtx on finalRef, we have to do this little dance of setting it (so it's in the LRU)
	// and then getting it+setting again.
	if err := bkcontenthash.SetCacheContext(ctx, finalRef, cacheCtx); err != nil {
		return nil, fmt.Errorf("failed to set cache context: %w", err)
	}
	cacheCtx, err = bkcontenthash.GetCacheContext(ctx, finalRef)
	if err != nil {
		return nil, fmt.Errorf("failed to get cache context: %w", err)
	}
	if err := bkcontenthash.SetCacheContext(ctx, finalRef, cacheCtx); err != nil {
		return nil, fmt.Errorf("failed to set cache context: %w", err)
	}

	if err := (contenthash.CacheRefMetadata{RefMetadata: finalRef}).SetContentHashKey(dgst); err != nil {
		return nil, fmt.Errorf("failed to set content hash key: %w", err)
	}
	if err := finalRef.SetDescription(fmt.Sprintf("local dir %s (include: %v) (exclude %v)", local.subdir, local.includes, local.excludes)); err != nil {
		return nil, fmt.Errorf("failed to set description: %w", err)
	}

	if err := finalRef.SetCachePolicyRetain(); err != nil {
		return nil, fmt.Errorf("failed to set cache policy: %w", err)
	}
	// NOTE: this MUST be released after setting cache policy retain or bk cache manager decides to
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
	return filepath.Join(local.rootPath, local.subdir, path)
}

// the absolute path under local.rootPath
func (local *localFS) toRootPath(path string) string {
	return filepath.Join(local.subdir, path)
}

// TODO: comment on the logic here, a bit subtle (i.e. why we don't verifyExpectedChange)
func (local *localFS) GetPreviousChange(ctx context.Context, path string, stat *types.Stat) (*CachedResult[string, *ChangeWithStat], error) {
	rootPath := local.toRootPath(path)
	return local.g.Do(ctx, rootPath, func(_ context.Context) (*ChangeWithStat, error) {
		fullPath := local.toFullPath(path)

		isRegular := stat.Mode&uint32(os.ModeType) == 0
		if isRegular {
			dgstBytes, err := sysx.Getxattr(fullPath, hashXattrKey)
			if err != nil {
				return nil, fmt.Errorf("failed to get content hash xattr: %w", err)
			}
			return &ChangeWithStat{
				kind: ChangeKindNone,
				stat: &HashedStatInfo{
					StatInfo: StatInfo{stat},
					dgst:     digest.Digest(dgstBytes),
				},
			}, nil
		}

		return &ChangeWithStat{
			kind: ChangeKindNone,
			stat: &HashedStatInfo{
				StatInfo: StatInfo{stat},
				dgst:     digest.NewDigest(XXH3, newHashFromStat(stat)),
			},
		}, nil
	})
}

func (local *localFS) RemoveAll(ctx context.Context, path string) (*CachedResult[string, *ChangeWithStat], error) {
	appliedChange, err := local.g.Do(ctx, local.toRootPath(path), func(ctx context.Context) (*ChangeWithStat, error) {
		fullPath := local.toFullPath(path)
		if err := os.RemoveAll(fullPath); err != nil {
			return nil, err
		}
		return &ChangeWithStat{kind: ChangeKindDelete}, nil
	})
	if err != nil {
		return nil, err
	}

	if err := verifyExpectedChange(path, appliedChange.Result(), ChangeKindDelete, nil); err != nil {
		appliedChange.Release()
		return nil, err
	}
	return appliedChange, nil
}

func (local *localFS) Mkdir(ctx context.Context, expectedChangeKind ChangeKind, path string, upperStat *types.Stat) (*CachedResult[string, *ChangeWithStat], error) {
	appliedChange, err := local.g.Do(ctx, local.toRootPath(path), func(ctx context.Context) (*ChangeWithStat, error) {
		fullPath := local.toFullPath(path)

		lowerStat, err := os.Lstat(fullPath)
		if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("failed to stat existing path: %w", err)
		}

		isNewDir := lowerStat == nil
		replacesNonDir := lowerStat != nil && !lowerStat.IsDir()

		if replacesNonDir {
			if err := os.Remove(fullPath); err != nil {
				return nil, fmt.Errorf("failed to remove existing file: %w", err)
			}
		}

		if isNewDir || replacesNonDir {
			if err := os.Mkdir(fullPath, os.FileMode(upperStat.Mode)&os.ModePerm); err != nil {
				return nil, fmt.Errorf("failed to create directory: %w", err)
			}
		}

		if err := rewriteMetadata(fullPath, upperStat); err != nil {
			return nil, fmt.Errorf("failed to rewrite directory metadata: %w", err)
		}

		return &ChangeWithStat{
			kind: expectedChangeKind,
			stat: &HashedStatInfo{
				StatInfo: StatInfo{upperStat},
				dgst:     digest.NewDigest(XXH3, newHashFromStat(upperStat)),
			},
		}, nil
	})
	if err != nil {
		return nil, err
	}

	if err := verifyExpectedChange(path, appliedChange.Result(), expectedChangeKind, upperStat); err != nil {
		appliedChange.Release()
		return nil, err
	}
	return appliedChange, nil
}

func (local *localFS) Symlink(ctx context.Context, expectedChangeKind ChangeKind, path string, upperStat *types.Stat) (*CachedResult[string, *ChangeWithStat], error) {
	appliedChange, err := local.g.Do(ctx, local.toRootPath(path), func(ctx context.Context) (*ChangeWithStat, error) {
		fullPath := local.toFullPath(path)

		lowerStat, err := os.Lstat(fullPath)
		if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("failed to stat existing path: %w", err)
		}

		isNewSymlink := lowerStat == nil

		if !isNewSymlink {
			if err := os.RemoveAll(fullPath); err != nil {
				return nil, fmt.Errorf("failed to remove existing file: %w", err)
			}
		}

		if err := os.Symlink(upperStat.Linkname, fullPath); err != nil {
			return nil, fmt.Errorf("failed to create symlink: %w", err)
		}

		return &ChangeWithStat{
			kind: expectedChangeKind,
			stat: &HashedStatInfo{
				StatInfo: StatInfo{upperStat},
				dgst:     digest.NewDigest(XXH3, newHashFromStat(upperStat)),
			},
		}, nil
	})
	if err != nil {
		return nil, err
	}

	if err := verifyExpectedChange(path, appliedChange.Result(), expectedChangeKind, upperStat); err != nil {
		appliedChange.Release()
		return nil, err
	}
	return appliedChange, nil
}

func (local *localFS) Hardlink(ctx context.Context, expectedChangeKind ChangeKind, path string, upperStat *types.Stat) (*CachedResult[string, *ChangeWithStat], error) {
	appliedChange, err := local.g.Do(ctx, local.toRootPath(path), func(ctx context.Context) (*ChangeWithStat, error) {
		fullPath := local.toFullPath(path)

		lowerStat, err := os.Lstat(fullPath)
		if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("failed to stat existing path: %w", err)
		}

		replacesExisting := lowerStat != nil

		if replacesExisting {
			if err := os.RemoveAll(fullPath); err != nil {
				return nil, fmt.Errorf("failed to remove existing file: %w", err)
			}
		}

		if err := os.Link(local.toFullPath(upperStat.Linkname), fullPath); err != nil {
			return nil, fmt.Errorf("failed to create hardlink: %w", err)
		}

		return &ChangeWithStat{
			kind: expectedChangeKind,
			stat: &HashedStatInfo{
				StatInfo: StatInfo{upperStat},
				dgst:     digest.NewDigest(XXH3, newHashFromStat(upperStat)),
			},
		}, nil
	})
	if err != nil {
		return nil, err
	}

	if err := verifyExpectedChange(path, appliedChange.Result(), expectedChangeKind, upperStat); err != nil {
		appliedChange.Release()
		return nil, err
	}
	return appliedChange, nil
}

var copyBufferPool = &sync.Pool{
	New: func() interface{} {
		buffer := make([]byte, 32*1024) // same default size as io.Copy
		return &buffer
	},
}

func (local *localFS) WriteFile(ctx context.Context, expectedChangeKind ChangeKind, path string, upperStat *types.Stat, upperFS ReadFS) (*CachedResult[string, *ChangeWithStat], error) {
	appliedChange, err := local.g.Do(ctx, local.toRootPath(path), func(ctx context.Context) (*ChangeWithStat, error) {
		reader, err := upperFS.ReadFile(ctx, path)
		if err != nil {
			return nil, fmt.Errorf("failed to read file %q: %w", path, err)
		}
		defer reader.Close()

		fullPath := local.toFullPath(path)

		lowerStat, err := os.Lstat(fullPath)
		if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("failed to stat existing path: %w", err)
		}

		replacesExisting := lowerStat != nil

		if replacesExisting {
			if err := os.RemoveAll(fullPath); err != nil {
				return nil, fmt.Errorf("failed to remove existing file: %w", err)
			}
		}

		f, err := os.OpenFile(fullPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(upperStat.Mode)&os.ModePerm)
		if err != nil {
			return nil, err
		}
		defer f.Close()

		h := newHashFromStat(upperStat)

		copyBuf := copyBufferPool.Get().(*[]byte)
		_, err = io.CopyBuffer(io.MultiWriter(f, h), reader, *copyBuf)
		copyBufferPool.Put(copyBuf)
		if err != nil {
			return nil, fmt.Errorf("failed to copy contents: %w", err)
		}
		if err := f.Close(); err != nil {
			return nil, fmt.Errorf("failed to close file: %w", err)
		}

		if err := rewriteMetadata(fullPath, upperStat); err != nil {
			return nil, fmt.Errorf("failed to rewrite file metadata: %w", err)
		}

		dgst := digest.NewDigest(XXH3, h)
		if err := sysx.Setxattr(fullPath, hashXattrKey, []byte(dgst.String()), 0); err != nil {
			return nil, fmt.Errorf("failed to set content hash xattr: %w", err)
		}

		return &ChangeWithStat{
			kind: expectedChangeKind,
			stat: &HashedStatInfo{
				StatInfo: StatInfo{upperStat},
				dgst:     dgst,
			},
		}, nil
	})
	if err != nil {
		return nil, err
	}

	if err := verifyExpectedChange(path, appliedChange.Result(), expectedChangeKind, upperStat); err != nil {
		appliedChange.Release()
		return nil, err
	}
	return appliedChange, nil
}

func (local *localFS) Walk(ctx context.Context, path string, walkFn fs.WalkDirFunc) error {
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

func (s *StatInfo) Type() fs.FileMode {
	return fs.FileMode(s.Stat.Mode)
}

func (s *StatInfo) Info() (fs.FileInfo, error) {
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

func verifyExpectedChange(path string, appliedChange *ChangeWithStat, expectedKind ChangeKind, expectedStat *types.Stat) error {
	if appliedChange.kind == ChangeKindDelete || expectedKind == ChangeKindDelete {
		if appliedChange.kind != expectedKind {
			return &ErrConflict{Path: path, FieldName: "change kind", OldVal: changeKindString(appliedChange.kind), NewVal: expectedKind.String()}
		}
		// nothing else to compare for deletes
		return nil
	}

	if uint32(appliedChange.stat.Mode()) != expectedStat.Mode {
		return &ErrConflict{Path: path, FieldName: "mode", OldVal: fmt.Sprintf("%o", appliedChange.stat.Mode()), NewVal: fmt.Sprintf("%o", expectedStat.Mode)}
	}
	if appliedChange.stat.Uid != expectedStat.Uid {
		return &ErrConflict{Path: path, FieldName: "uid", OldVal: fmt.Sprintf("%d", appliedChange.stat.Uid), NewVal: fmt.Sprintf("%d", expectedStat.Uid)}
	}
	if appliedChange.stat.Gid != expectedStat.Gid {
		return &ErrConflict{Path: path, FieldName: "gid", OldVal: fmt.Sprintf("%d", appliedChange.stat.Gid), NewVal: fmt.Sprintf("%d", expectedStat.Gid)}
	}
	if appliedChange.stat.Size_ != expectedStat.Size_ {
		return &ErrConflict{Path: path, FieldName: "size", OldVal: fmt.Sprintf("%d", appliedChange.stat.Size()), NewVal: fmt.Sprintf("%d", expectedStat.Size_)}
	}
	if appliedChange.stat.Devmajor != expectedStat.Devmajor {
		return &ErrConflict{Path: path, FieldName: "devmajor", OldVal: fmt.Sprintf("%d", appliedChange.stat.Devmajor), NewVal: fmt.Sprintf("%d", expectedStat.Devmajor)}
	}
	if appliedChange.stat.Devminor != expectedStat.Devminor {
		return &ErrConflict{Path: path, FieldName: "devminor", OldVal: fmt.Sprintf("%d", appliedChange.stat.Devminor), NewVal: fmt.Sprintf("%d", expectedStat.Devminor)}
	}

	// Only compare link name when it's a symlink, not a hardlink. For hardlinks, whether the Linkname field
	// is set depends on whether or not the source of the link was included in the sync, which can vary between
	// different include/exclude settings on the same dir.
	if appliedChange.stat.Mode()&os.ModeType == os.ModeSymlink {
		if appliedChange.stat.Linkname != expectedStat.Linkname {
			return &ErrConflict{Path: path, FieldName: "linkname", OldVal: appliedChange.stat.Linkname, NewVal: expectedStat.Linkname}
		}
	}

	// Match the differ logic by only comparing modtime for regular files (as a heuristic to
	// expensive avoid content comparisons for every file that appears in a diff, using the
	// modtime as a proxy instead).
	//
	// We don't want to compare modtimes for directories right now since we explicitly don't
	// attempt to reset parent dir modtimes when a dirent is synced in or removed.
	if appliedChange.stat.Mode().IsRegular() {
		if appliedChange.stat.ModTime().UnixNano() != expectedStat.ModTime {
			return &ErrConflict{Path: path, FieldName: "mod time", OldVal: fmt.Sprintf("%d", appliedChange.stat.ModTime().UnixNano()), NewVal: fmt.Sprintf("%d", expectedStat.ModTime)}
		}
	}

	return nil
}

type ErrConflict struct {
	Path      string
	FieldName string
	OldVal    string
	NewVal    string
}

func (e *ErrConflict) Error() string {
	return fmt.Sprintf("conflict at %q: %s changed from %q to %q during sync", e.Path, e.FieldName, e.OldVal, e.NewVal)
}
