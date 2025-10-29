package filesync

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/tonistiigi/fsutil"
	"github.com/tonistiigi/fsutil/types"
	"golang.org/x/sync/errgroup"
)

type ChangeKind = fsutil.ChangeKind

const (
	ChangeKindAdd    ChangeKind = fsutil.ChangeKindAdd
	ChangeKindModify ChangeKind = fsutil.ChangeKindModify
	ChangeKindDelete ChangeKind = fsutil.ChangeKindDelete
	ChangeKindNone   ChangeKind = fsutil.ChangeKindDelete + 1
)

func changeKindString(kind ChangeKind) string {
	switch kind {
	case ChangeKindNone:
		return "none"
	default:
		return kind.String()
	}
}

type ChangeFunc func(kind ChangeKind, path string, lowerStat, upperStat *types.Stat) error

// This is based on a combination of containerd's diff implementation and buildkit's (which is itself
// based on containerd's). Main change is to accept WalkFS interfaces.
// https://github.com/tonistiigi/fsutil/blob/0789dc562bd7099bec7be479164e261ac5334f5f/diff_containerd.go#L15
// https://github.com/containerd/continuity/blob/44e2adf7e9cd87330f3ad656e7a006ef91ed8c1e/fs/diff.go#L273
//
// The gist of the idea is that the lower+upper filesystems are walked in the same order with the same
// include/exclude rules applied. For a given walk iteration:
// - If the next paths from each walk are not the same, there must be either an addition or deletion
// - If the next paths from each walk *are* the same, then stats are compared to determine if any changes exist between the two
//
// When comparing regular files, the file size + modtime are used as a proxy for file contents, which avoids the client needing
// to make expensive file content hashes everytime.
func doubleWalkDiff(ctx context.Context, eg *errgroup.Group, lower, upper WalkFS, changeFn ChangeFunc) {
	var (
		lowerPathCh = make(chan *currentPath, 128)
		upperPathCh = make(chan *currentPath, 128)

		lowerPath, upperPath *currentPath
		rmdir                string
	)
	eg.Go(func() error {
		defer close(lowerPathCh)
		return pathWalk(ctx, lower, lowerPathCh)
	})
	eg.Go(func() error {
		defer close(upperPathCh)
		return pathWalk(ctx, upper, upperPathCh)
	})
	eg.Go(func() error {
		var err error
		for lowerPathCh != nil || upperPathCh != nil {
			if lowerPath == nil && lowerPathCh != nil {
				lowerPath, err = nextPath(ctx, lowerPathCh)
				if err != nil {
					return err
				}
				if lowerPath == nil {
					lowerPathCh = nil
				}
			}

			if upperPath == nil && upperPathCh != nil {
				upperPath, err = nextPath(ctx, upperPathCh)
				if err != nil {
					return err
				}
				if upperPath == nil {
					upperPathCh = nil
				}
			}
			if lowerPath == nil && upperPath == nil {
				continue
			}

			var upperStat, lowerStat *types.Stat
			k, p := pathChange(lowerPath, upperPath)
			switch k {
			case ChangeKindAdd:
				if rmdir != "" {
					rmdir = ""
				}
				upperStat = upperPath.stat
				upperPath = nil
			case ChangeKindDelete:
				// Check if this file is already removed by being
				// under of a removed directory
				if rmdir != "" && strings.HasPrefix(lowerPath.path, rmdir) {
					lowerPath = nil
					continue
				} else if lowerPath.stat.IsDir() {
					rmdir = lowerPath.path + string(os.PathSeparator)
				} else if rmdir != "" {
					rmdir = ""
				}
				lowerStat = lowerPath.stat
				lowerPath = nil
			case ChangeKindModify:
				same := sameFile(lowerPath.stat, upperPath.stat)
				if lowerPath.stat.IsDir() && !upperPath.stat.IsDir() {
					rmdir = lowerPath.path + string(os.PathSeparator)
				} else if rmdir != "" {
					rmdir = ""
				}
				upperStat = upperPath.stat
				lowerStat = lowerPath.stat
				lowerPath = nil
				upperPath = nil
				if same {
					k = ChangeKindNone
				}
			}
			if err := changeFn(k, p, lowerStat, upperStat); err != nil {
				return err
			}
		}
		return nil
	})
}

func pathChange(lower, upper *currentPath) (ChangeKind, string) {
	if lower == nil {
		if upper == nil {
			panic("cannot compare nil paths")
		}
		return ChangeKindAdd, upper.path
	}
	if upper == nil {
		return ChangeKindDelete, lower.path
	}

	switch i := comparePath(lower.path, upper.path); {
	case i < 0:
		// File in lower that is not in upper
		return ChangeKindDelete, lower.path
	case i > 0:
		// File in upper that is not in lower
		return ChangeKindAdd, upper.path
	default:
		return ChangeKindModify, upper.path
	}
}

func comparePath(p1, p2 string) int {
	minLenPath := min(len(p1), len(p2))
	for i := range minLenPath {
		switch {
		case p1[i] == p2[i]:
			continue
		case p2[i] != filepath.Separator && p1[i] < p2[i] || p1[i] == filepath.Separator:
			return -1
		default:
			return 1
		}
	}
	return len(p1) - len(p2)
}

func sameFile(f1, f2 *types.Stat) bool {
	// If not a directory also check size, modtime
	if !f1.IsDir() {
		if f1.Size_ != f2.Size_ {
			return false
		}

		if f1.ModTime != f2.ModTime {
			return false
		}
	}

	return f1.Mode == f2.Mode &&
		f1.Uid == f2.Uid &&
		f1.Gid == f2.Gid &&
		f1.Devmajor == f2.Devmajor &&
		f1.Devminor == f2.Devminor &&
		f1.Linkname == f2.Linkname
}

type currentPath struct {
	path string
	stat *types.Stat
}

func pathWalk(ctx context.Context, walkFS WalkFS, pathC chan<- *currentPath) error {
	return walkFS.Walk(ctx, "/", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		info, err := d.Info()
		if err != nil {
			return err
		}
		stat, ok := info.Sys().(*types.Stat)
		if !ok {
			return fmt.Errorf("fileinfo without stat info")
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case pathC <- &currentPath{path: path, stat: stat}:
			return nil
		}
	})
}

func nextPath(ctx context.Context, pathC <-chan *currentPath) (*currentPath, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case p := <-pathC:
		return p, nil
	}
}
