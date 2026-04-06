//go:build linux
// +build linux

package fsdiff

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	continuityfs "github.com/containerd/continuity/fs"
	"golang.org/x/sync/errgroup"
)

type currentPath struct {
	path     string
	f        os.FileInfo
	fullPath string
}

func pathChange(lower, upper *currentPath) (continuityfs.ChangeKind, string) {
	if lower == nil {
		if upper == nil {
			panic("cannot compare nil paths")
		}
		return continuityfs.ChangeKindAdd, upper.path
	}
	if upper == nil {
		return continuityfs.ChangeKindDelete, lower.path
	}

	switch i := directoryCompare(lower.path, upper.path); {
	case i < 0:
		return continuityfs.ChangeKindDelete, lower.path
	case i > 0:
		return continuityfs.ChangeKindAdd, upper.path
	default:
		return continuityfs.ChangeKindModify, upper.path
	}
}

func directoryCompare(a, b string) int {
	l := len(a)
	if len(b) < l {
		l = len(b)
	}
	for i := 0; i < l; i++ {
		c1, c2 := a[i], b[i]
		if c1 == filepath.Separator {
			c1 = byte(0)
		}
		if c2 == filepath.Separator {
			c2 = byte(0)
		}
		if c1 < c2 {
			return -1
		}
		if c1 > c2 {
			return +1
		}
	}
	if len(a) < len(b) {
		return -1
	}
	if len(a) > len(b) {
		return +1
	}
	return 0
}

func pathWalk(ctx context.Context, root string, pathC chan<- *currentPath) error {
	return filepath.Walk(root, func(path string, f os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		path, err = filepath.Rel(root, path)
		if err != nil {
			return err
		}

		path = filepath.Join(string(os.PathSeparator), path)
		if path == string(os.PathSeparator) {
			return nil
		}

		p := &currentPath{
			path:     path,
			f:        f,
			fullPath: filepath.Join(root, path),
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case pathC <- p:
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

func addDirChanges(ctx context.Context, changeFn continuityfs.ChangeFunc, root string) error {
	return filepath.Walk(root, func(path string, f os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		path, err = filepath.Rel(root, path)
		if err != nil {
			return err
		}

		path = filepath.Join(string(os.PathSeparator), path)
		if path == string(os.PathSeparator) {
			return nil
		}

		return changeFn(continuityfs.ChangeKindAdd, path, f, nil)
	})
}

func WalkChanges(
	ctx context.Context,
	lowerRoot, upperRoot string,
	comparison Comparison,
	changeFn continuityfs.ChangeFunc,
) error {
	if lowerRoot == "" {
		return addDirChanges(ctx, changeFn, upperRoot)
	}
	return doubleWalkChanges(ctx, changeFn, lowerRoot, upperRoot, comparison)
}

func doubleWalkChanges(
	ctx context.Context,
	changeFn continuityfs.ChangeFunc,
	lowerRoot, upperRoot string,
	comparison Comparison,
) (err error) {
	g, ctx := errgroup.WithContext(ctx)

	var (
		c1 = make(chan *currentPath)
		c2 = make(chan *currentPath)

		f1, f2 *currentPath
		rmdir  string
	)
	g.Go(func() error {
		defer close(c1)
		return pathWalk(ctx, lowerRoot, c1)
	})
	g.Go(func() error {
		defer close(c2)
		return pathWalk(ctx, upperRoot, c2)
	})
	g.Go(func() error {
		for c1 != nil || c2 != nil {
			if f1 == nil && c1 != nil {
				f1, err = nextPath(ctx, c1)
				if err != nil {
					return err
				}
				if f1 == nil {
					c1 = nil
				}
			}

			if f2 == nil && c2 != nil {
				f2, err = nextPath(ctx, c2)
				if err != nil {
					return err
				}
				if f2 == nil {
					c2 = nil
				}
			}
			if f1 == nil && f2 == nil {
				continue
			}

			var f os.FileInfo
			k, p := pathChange(f1, f2)
			switch k {
			case continuityfs.ChangeKindAdd:
				if rmdir != "" {
					rmdir = ""
				}
				f = f2.f
				f2 = nil
			case continuityfs.ChangeKindDelete:
				if rmdir != "" && strings.HasPrefix(f1.path, rmdir) {
					f1 = nil
					continue
				} else if f1.f.IsDir() {
					rmdir = f1.path + string(os.PathSeparator)
				} else if rmdir != "" {
					rmdir = ""
				}
				f1 = nil
			case continuityfs.ChangeKindModify:
				same, err := samePathInfo(f1.f, f2.f, f1.fullPath, f2.fullPath, comparison)
				if err != nil {
					return err
				}
				if f1.f.IsDir() && !f2.f.IsDir() {
					rmdir = f1.path + string(os.PathSeparator)
				} else if rmdir != "" {
					rmdir = ""
				}
				f = f2.f
				f1 = nil
				f2 = nil
				if same {
					if !isLinked(f) {
						continue
					}
					k = continuityfs.ChangeKindUnmodified
				}
			}
			if err := changeFn(k, p, f, nil); err != nil {
				return err
			}
		}
		return nil
	})

	return g.Wait()
}
