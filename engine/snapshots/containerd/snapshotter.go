package containerd

import (
	"context"
	"os"
	"strings"
	"sync"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/dagger/dagger/engine/snapshots/snapshotter"
	"github.com/moby/sys/userns"
	"github.com/pkg/errors"
)

func NewSnapshotter(name string, sn snapshots.Snapshotter, ns string) snapshotter.Snapshotter {
	return &fromContainerd{name: name, Snapshotter: &nsSnapshotter{ns: ns, Snapshotter: sn}}
}

type nsSnapshotter struct {
	ns string
	snapshots.Snapshotter
}

func (s *nsSnapshotter) Stat(ctx context.Context, key string) (snapshots.Info, error) {
	ctx = namespaces.WithNamespace(ctx, s.ns)
	return s.Snapshotter.Stat(ctx, key)
}

func (s *nsSnapshotter) Update(ctx context.Context, info snapshots.Info, fieldpaths ...string) (snapshots.Info, error) {
	ctx = namespaces.WithNamespace(ctx, s.ns)
	return s.Snapshotter.Update(ctx, info, fieldpaths...)
}

func (s *nsSnapshotter) Usage(ctx context.Context, key string) (snapshots.Usage, error) {
	ctx = namespaces.WithNamespace(ctx, s.ns)
	return s.Snapshotter.Usage(ctx, key)
}

func (s *nsSnapshotter) Mounts(ctx context.Context, key string) ([]mount.Mount, error) {
	ctx = namespaces.WithNamespace(ctx, s.ns)
	return s.Snapshotter.Mounts(ctx, key)
}

func (s *nsSnapshotter) Prepare(ctx context.Context, key, parent string, opts ...snapshots.Opt) ([]mount.Mount, error) {
	ctx = namespaces.WithNamespace(ctx, s.ns)
	return s.Snapshotter.Prepare(ctx, key, parent, opts...)
}

func (s *nsSnapshotter) View(ctx context.Context, key, parent string, opts ...snapshots.Opt) ([]mount.Mount, error) {
	ctx = namespaces.WithNamespace(ctx, s.ns)
	return s.Snapshotter.View(ctx, key, parent, opts...)
}

func (s *nsSnapshotter) Commit(ctx context.Context, name, key string, opts ...snapshots.Opt) error {
	ctx = namespaces.WithNamespace(ctx, s.ns)
	return s.Snapshotter.Commit(ctx, name, key, opts...)
}

func (s *nsSnapshotter) Remove(ctx context.Context, key string) error {
	return errors.Errorf("calling snapshotter.Remove is forbidden")
}

func (s *nsSnapshotter) Walk(ctx context.Context, fn snapshots.WalkFunc, filters ...string) error {
	ctx = namespaces.WithNamespace(ctx, s.ns)
	return s.Snapshotter.Walk(ctx, fn, filters...)
}

type fromContainerd struct {
	name string
	snapshots.Snapshotter
}

func (s *fromContainerd) Name() string {
	return s.name
}

func (s *fromContainerd) Mounts(ctx context.Context, key string) (snapshotter.Mountable, error) {
	mounts, err := s.Snapshotter.Mounts(ctx, key)
	if err != nil {
		return nil, err
	}
	return &staticMountable{mounts: mounts, id: key}, nil
}

func (s *fromContainerd) Prepare(ctx context.Context, key, parent string, opts ...snapshots.Opt) error {
	_, err := s.Snapshotter.Prepare(ctx, key, parent, opts...)
	return err
}

func (s *fromContainerd) View(ctx context.Context, key, parent string, opts ...snapshots.Opt) (snapshotter.Mountable, error) {
	mounts, err := s.Snapshotter.View(ctx, key, parent, opts...)
	if err != nil {
		return nil, err
	}
	return &staticMountable{mounts: mounts, id: key}, nil
}

func (s *fromContainerd) Commit(ctx context.Context, name, key string, opts ...snapshots.Opt) error {
	info, err := s.Stat(ctx, key)
	if err != nil {
		return errors.Wrap(err, "failed to stat active key during commit")
	}
	opts = append(opts, snapshots.WithLabels(snapshots.FilterInheritedLabels(info.Labels)))
	return s.Snapshotter.Commit(ctx, name, key, opts...)
}

func NewContainerdSnapshotter(s snapshotter.Snapshotter) (snapshots.Snapshotter, func() error) {
	cs := &containerdSnapshotter{Snapshotter: s}
	return cs, cs.release
}

type containerdSnapshotter struct {
	mu        sync.Mutex
	releasers []func() error
	snapshotter.Snapshotter
}

func (cs *containerdSnapshotter) release() error {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	var err error
	for _, f := range cs.releasers {
		if err1 := f(); err1 != nil && err == nil {
			err = err1
		}
	}
	return err
}

func (cs *containerdSnapshotter) returnMounts(mf snapshotter.Mountable) ([]mount.Mount, error) {
	mounts, release, err := mf.Mount()
	if err != nil {
		return nil, err
	}
	cs.mu.Lock()
	cs.releasers = append(cs.releasers, release)
	cs.mu.Unlock()
	redirectDirOption := getRedirectDirOption()
	if redirectDirOption != "" {
		mounts = setRedirectDir(mounts, redirectDirOption)
	}
	return mounts, nil
}

func (cs *containerdSnapshotter) Mounts(ctx context.Context, key string) ([]mount.Mount, error) {
	mf, err := cs.Snapshotter.Mounts(ctx, key)
	if err != nil {
		return nil, err
	}
	return cs.returnMounts(mf)
}

func (cs *containerdSnapshotter) Prepare(ctx context.Context, key, parent string, opts ...snapshots.Opt) ([]mount.Mount, error) {
	if err := cs.Snapshotter.Prepare(ctx, key, parent, opts...); err != nil {
		return nil, err
	}
	return cs.Mounts(ctx, key)
}

func (cs *containerdSnapshotter) View(ctx context.Context, key, parent string, opts ...snapshots.Opt) ([]mount.Mount, error) {
	mf, err := cs.Snapshotter.View(ctx, key, parent, opts...)
	if err != nil {
		return nil, err
	}
	return cs.returnMounts(mf)
}

type staticMountable struct {
	id     string
	mounts []mount.Mount
}

func (cm *staticMountable) Mount() ([]mount.Mount, func() error, error) {
	mounts := make([]mount.Mount, len(cm.mounts))
	copy(mounts, cm.mounts)

	redirectDirOption := getRedirectDirOption()
	if redirectDirOption != "" {
		mounts = setRedirectDir(mounts, redirectDirOption)
	}

	return mounts, func() error {
		return nil
	}, nil
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
