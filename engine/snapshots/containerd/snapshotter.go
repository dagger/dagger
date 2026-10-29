package containerd

import (
	"context"
	"os"
	"strings"
	"sync"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/core/snapshots"
	bksnapshots "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/wcprof"
	"github.com/moby/sys/userns"
	"github.com/pkg/errors"
)

func NewSnapshotter(name string, sn snapshots.Snapshotter, ns string) bksnapshots.Snapshotter {
	return &fromContainerd{name: name, Snapshotter: &nsSnapshotter{ns: ns, Snapshotter: sn}}
}

type nsSnapshotter struct {
	ns string
	snapshots.Snapshotter
}

// beginSnapshotOp begins an op for one call to containerd's snapshotter on the
// snapshot key, recorded only when ctx is profiled.
func beginSnapshotOp(ctx context.Context, call, key string) *wcprof.Op {
	_, op := wcprof.BeginOp(ctx, wcprof.OpKindInternal, "snapshot."+call, wcprof.OpOpts{Ident: key})
	return op
}

func (s *nsSnapshotter) Stat(ctx context.Context, key string) (snapshots.Info, error) {
	ctx = bksnapshots.WithNamespace(ctx, s.ns)
	return s.Snapshotter.Stat(ctx, key)
}

func (s *nsSnapshotter) Update(ctx context.Context, info snapshots.Info, fieldpaths ...string) (snapshots.Info, error) {
	ctx = bksnapshots.WithNamespace(ctx, s.ns)
	return s.Snapshotter.Update(ctx, info, fieldpaths...)
}

func (s *nsSnapshotter) Usage(ctx context.Context, key string) (snapshots.Usage, error) {
	ctx = bksnapshots.WithNamespace(ctx, s.ns)
	op := beginSnapshotOp(ctx, "usage", key)
	usage, err := s.Snapshotter.Usage(ctx, key)
	op.EndErr(err)
	return usage, err
}

func (s *nsSnapshotter) Mounts(ctx context.Context, key string) ([]mount.Mount, error) {
	ctx = bksnapshots.WithNamespace(ctx, s.ns)
	op := beginSnapshotOp(ctx, "mounts", key)
	mounts, err := s.Snapshotter.Mounts(ctx, key)
	op.EndErr(err)
	return mounts, err
}

func (s *nsSnapshotter) Prepare(ctx context.Context, key, parent string, opts ...snapshots.Opt) ([]mount.Mount, error) {
	ctx = bksnapshots.WithNamespace(ctx, s.ns)
	op := beginSnapshotOp(ctx, "prepare", key)
	mounts, err := s.Snapshotter.Prepare(ctx, key, parent, opts...)
	op.EndErr(err)
	return mounts, err
}

func (s *nsSnapshotter) View(ctx context.Context, key, parent string, opts ...snapshots.Opt) ([]mount.Mount, error) {
	ctx = bksnapshots.WithNamespace(ctx, s.ns)
	op := beginSnapshotOp(ctx, "view", key)
	mounts, err := s.Snapshotter.View(ctx, key, parent, opts...)
	op.EndErr(err)
	return mounts, err
}

func (s *nsSnapshotter) Commit(ctx context.Context, name, key string, opts ...snapshots.Opt) error {
	ctx = bksnapshots.WithNamespace(ctx, s.ns)
	op := beginSnapshotOp(ctx, "commit", name)
	err := s.Snapshotter.Commit(ctx, name, key, opts...)
	op.EndErr(err)
	return err
}

func (s *nsSnapshotter) Remove(ctx context.Context, key string) error {
	return errors.Errorf("calling snapshotter.Remove is forbidden")
}

func (s *nsSnapshotter) Walk(ctx context.Context, fn snapshots.WalkFunc, filters ...string) error {
	ctx = bksnapshots.WithNamespace(ctx, s.ns)
	return s.Snapshotter.Walk(ctx, fn, filters...)
}

type fromContainerd struct {
	name string
	snapshots.Snapshotter
}

func (s *fromContainerd) Name() string {
	return s.name
}

func (s *fromContainerd) Mounts(ctx context.Context, key string) (bksnapshots.MountableRef, error) {
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

func (s *fromContainerd) View(ctx context.Context, key, parent string, opts ...snapshots.Opt) (bksnapshots.MountableRef, error) {
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

func NewContainerdSnapshotter(s bksnapshots.Snapshotter) (snapshots.Snapshotter, func() error) {
	cs := &containerdSnapshotter{Snapshotter: s}
	return cs, cs.release
}

type containerdSnapshotter struct {
	mu        sync.Mutex
	releasers []func() error
	bksnapshots.Snapshotter
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

func (cs *containerdSnapshotter) returnMounts(mf bksnapshots.MountableRef) ([]mount.Mount, error) {
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
