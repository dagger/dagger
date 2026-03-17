package filesync

import (
	"context"
	"io"
	"io/fs"
)

type WalkFS interface {
	Walk(ctx context.Context, path string, walkFn fs.WalkDirFunc) error
}

type ReadFS interface {
	WalkFS
	ReadFile(ctx context.Context, path string) (io.ReadCloser, error)
}
