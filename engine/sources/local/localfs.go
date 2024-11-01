package local

import (
	"context"
	"fmt"
	"hash"
	"io"
	gofs "io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/containerd/continuity/sysx"
	"github.com/moby/buildkit/cache/contenthash"
	"github.com/moby/buildkit/util/bklog"
	"github.com/moby/buildkit/util/flightcontrol"
	digest "github.com/opencontainers/go-digest"
	"github.com/tonistiigi/fsutil"
	"github.com/tonistiigi/fsutil/types"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sys/unix"
)

type localFSSharedState struct {
	rootPath      string
	contentHasher func(ChangeKind, string, os.FileInfo, error) error
	g             flightcontrol.Group[*struct{}]
}

type localFS struct {
	*localFSSharedState

	subdir string

	filterFS fsutil.FS
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
	}, nil
}

func (local *localFS) Sync(ctx context.Context, remote ReadFS) (rerr error) {
	eg, ctx := errgroup.WithContext(ctx)

	eg.Go(func() error {
		return doubleWalkDiff(ctx, local, remote, func(kind ChangeKind, path string, lowerStat, upperStat *types.Stat) error {
			// TODO:
			// TODO:
			// TODO:
			// TODO:
			bklog.G(ctx).Debugf("DIFF %s %s (%s) (%s)", kind, path, local.toRootPath(path), local.toFullPath(path))

			if kind == ChangeKindDelete {
				return local.RemoveAll(ctx, path)
			}

			// TODO: handle parent dir mod times
			switch {
			case upperStat.IsDir():
				return local.Mkdir(ctx, path, lowerStat, upperStat)
			case upperStat.Mode&uint32(os.ModeDevice) != 0 || upperStat.Mode&uint32(os.ModeNamedPipe) != 0:
				// TODO: return local.Mknode(ctx, path, gofs.FileMode(upperStat.Mode), uint32(upperStat.Devmajor), uint32(upperStat.Devminor))
			case upperStat.Mode&uint32(os.ModeSymlink) != 0:
				return local.Symlink(ctx, path, lowerStat, upperStat)
			case upperStat.Linkname != "":
				return local.Hardlink(ctx, path, lowerStat, upperStat)
			default:
				eg.Go(func() error {
					// TODO: DOUBLE CHECK IF YOU NEED TO COPY STAT OBJS SINCE THIS IS ASYNC
					return local.WriteFile(ctx, path, lowerStat, upperStat, remote)
				})
			}

			return nil
		})
	})

	return eg.Wait()
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

// TODO:
// TODO:
// TODO:
// TODO:
// Probably need atomic renames for everything to be 100% resilient against every possible race condition
// Although that can't handle replacing a file with a directory or vice versa, unless you can rely on RENAME_EXCHANGE
// Exists since 3.15? https://stackoverflow.com/questions/27862057/atomically-swap-contents-of-two-files-on-linux#comment130691510_27862160

func (local *localFS) mutate(
	ctx context.Context,
	path string,
	lowerStat, upperStat *types.Stat,
	fn func(ctx context.Context, fullPath string, h hash.Hash) error,
) error {
	rootPath := local.toRootPath(path)
	fullPath := local.toFullPath(path)
	_, err := local.g.Do(ctx, rootPath, func(ctx context.Context) (*struct{}, error) {
		switch {
		case upperStat != nil: // NOTE: at the moment contenthash doesn't care if it's Add vs. Modify
			h, err := contenthash.NewFromStat(upperStat)
			if err != nil {
				return nil, fmt.Errorf("failed to create content hash: %w", err)
			}
			err = fn(ctx, fullPath, h)
			if err != nil {
				return nil, err
			}
			hashStat := &HashedStatInfo{StatInfo{upperStat}, digest.NewDigest(digest.SHA256, h)}
			if err := local.contentHasher(ChangeKindAdd, rootPath, hashStat, nil); err != nil {
				return nil, err
			}

		default:
			err := fn(ctx, fullPath, nil)
			if err != nil {
				return nil, err
			}
			if err := local.contentHasher(ChangeKindDelete, rootPath, nil, nil); err != nil {
				return nil, err
			}
		}

		return nil, nil
	})
	return err
}

func (local *localFS) RemoveAll(ctx context.Context, path string) error {
	return local.mutate(ctx, path, nil, nil, func(ctx context.Context, fullPath string, _ hash.Hash) error {
		return os.RemoveAll(fullPath)
	})
}

func (local *localFS) Mkdir(ctx context.Context, path string, lowerStat, upperStat *types.Stat) error {
	return local.mutate(ctx, path, lowerStat, upperStat, func(ctx context.Context, fullPath string, _ hash.Hash) error {
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
}

func (local *localFS) Symlink(ctx context.Context, path string, lowerStat, upperStat *types.Stat) error {
	return local.mutate(ctx, path, lowerStat, upperStat, func(ctx context.Context, fullPath string, h hash.Hash) error {
		if lowerStat != nil {
			if err := os.RemoveAll(fullPath); err != nil {
				return fmt.Errorf("failed to remove existing file: %w", err)
			}
		}

		if err := os.Symlink(upperStat.Linkname, fullPath); err != nil {
			return fmt.Errorf("failed to create symlink: %w", err)
		}

		return nil
	})
}

func (local *localFS) Hardlink(ctx context.Context, path string, lowerStat, upperStat *types.Stat) error {
	return local.mutate(ctx, path, lowerStat, upperStat, func(ctx context.Context, fullPath string, h hash.Hash) error {
		if lowerStat != nil {
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
}

func (local *localFS) WriteFile(ctx context.Context, path string, lowerStat, upperStat *types.Stat, upperFS ReadFS) error {
	return local.mutate(ctx, path, lowerStat, upperStat, func(ctx context.Context, fullPath string, h hash.Hash) error {
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

		return nil
	})
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
