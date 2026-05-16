package snapshots

import (
	"context"

	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/dagger/dagger/internal/buildkit/executor"
)

type MountableRef = executor.MountableRef

type Snapshotter interface {
	Name() string
	Mounts(ctx context.Context, key string) (MountableRef, error)
	Prepare(ctx context.Context, key, parent string, opts ...snapshots.Opt) error
	View(ctx context.Context, key, parent string, opts ...snapshots.Opt) (MountableRef, error)

	Stat(ctx context.Context, key string) (snapshots.Info, error)
	Update(ctx context.Context, info snapshots.Info, fieldpaths ...string) (snapshots.Info, error)
	Usage(ctx context.Context, key string) (snapshots.Usage, error)
	Commit(ctx context.Context, name, key string, opts ...snapshots.Opt) error
	Remove(ctx context.Context, key string) error
	Walk(ctx context.Context, fn snapshots.WalkFunc, filters ...string) error
	Close() error
}
