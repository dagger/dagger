package snapshotter

import (
	"context"
	"os"
	"strings"
	"sync"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/dagger/dagger/internal/buildkit/executor"
	"github.com/moby/sys/userns"
)

type Mountable = executor.MountableRef

type Snapshotter interface {
	Name() string
	Mounts(ctx context.Context, key string) (Mountable, error)
	Prepare(ctx context.Context, key, parent string, opts ...snapshots.Opt) error
	View(ctx context.Context, key, parent string, opts ...snapshots.Opt) (Mountable, error)

	Stat(ctx context.Context, key string) (snapshots.Info, error)
	Update(ctx context.Context, info snapshots.Info, fieldpaths ...string) (snapshots.Info, error)
	Usage(ctx context.Context, key string) (snapshots.Usage, error)
	Commit(ctx context.Context, name, key string, opts ...snapshots.Opt) error
	Remove(ctx context.Context, key string) error
	Walk(ctx context.Context, fn snapshots.WalkFunc, filters ...string) error
	Close() error
}

var redirectDirOption string
var redirectDirOptionOnce sync.Once

func getRedirectDirOption() string {
	redirectDirOptionOnce.Do(func() {
		if _, err := os.Stat("/sys/module/overlay/parameters/redirect_dir"); err != nil {
			redirectDirOption = ""
			return
		}
		if userns.RunningInUserNS() {
			redirectDirOption = ""
			return
		}
		redirectDirOption = "off"
	})
	return redirectDirOption
}

func setRedirectDir(mounts []mount.Mount, redirectDirOption string) (ret []mount.Mount) {
	if redirectDirOption == "" {
		return mounts
	}
	for _, m := range mounts {
		if m.Type == "overlay" {
			var opts []string
			for _, o := range m.Options {
				if strings.HasPrefix(o, "redirect_dir=") {
					continue
				}
				opts = append(opts, o)
			}
			opts = append(opts, "redirect_dir="+redirectDirOption)
			m.Options = opts
		}
		ret = append(ret, m)
	}
	return ret
}
