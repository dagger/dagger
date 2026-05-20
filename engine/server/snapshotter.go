package server

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/containerd/containerd/v2/core/mount"
	ctdsnapshot "github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/core/snapshots/storage"
	"github.com/containerd/containerd/v2/plugins/snapshots/native"
	"github.com/containerd/containerd/v2/plugins/snapshots/overlay"
	"github.com/containerd/containerd/v2/plugins/snapshots/overlay/overlayutils"
	"github.com/dagger/dagger/engine/slog"
	bkconfig "github.com/dagger/dagger/internal/buildkit/cmd/buildkitd/config"
	"github.com/sirupsen/logrus"
)

func newSnapshotter(
	rootDir string,
	cfg bkconfig.OCIConfig,
	mdStore *storage.MetaStore,
) (ctdsnapshot.Snapshotter, string, error) {
	configureBboltDefaults()

	var (
		name    = cfg.Snapshotter
		address = cfg.ProxySnapshotterPath
	)
	if address != "" {
		return nil, "", fmt.Errorf("proxy snapshotter is not supported")
	}

	if name == "" {
		if err := overlayutils.Supported(rootDir); err == nil {
			name = "overlayfs"
		} else {
			logrus.Debugf("auto snapshotter: overlayfs is not available for %s, falling back to native: %v", rootDir, err)
			name = "native"
		}
		logrus.Infof("auto snapshotter: using %s", name)
	}

	var sn ctdsnapshot.Snapshotter
	var snErr error
	switch name {
	case "native":
		sn, snErr = native.NewSnapshotter(rootDir)
	case "overlayfs": // not "overlay", for consistency with containerd snapshotter plugin ID.
		opts := []overlay.Opt{
			overlay.WithMetaStore(mdStore),
			overlay.AsynchronousRemove,
		}
		if overlayVolatileSupported(rootDir) {
			opts = append(opts, overlay.WithMountOptions([]string{"volatile"}))
		}
		sn, snErr = overlay.NewSnapshotter(rootDir, opts...)
	default:
		return nil, "", fmt.Errorf("unknown snapshotter %q", name)
	}
	if snErr != nil {
		return nil, "", fmt.Errorf("failed to create snapshotter %q: %w", name, snErr)
	}

	return sn, name, nil
}

var overlayVolatileOnce sync.Once
var overlayVolatileOK bool

func overlayVolatileSupported(rootDir string) bool {
	overlayVolatileOnce.Do(func() {
		ok, err := checkOverlayVolatile(rootDir)
		if err != nil {
			slog.Debug("overlayfs volatile option unavailable, skipping", "err", err)
		}
		overlayVolatileOK = ok
	})
	return overlayVolatileOK
}

func checkOverlayVolatile(rootDir string) (bool, error) {
	if err := os.MkdirAll(rootDir, 0700); err != nil {
		return false, err
	}
	td, err := os.MkdirTemp(rootDir, "overlay-volatile-check-")
	if err != nil {
		return false, err
	}
	defer os.RemoveAll(td)

	for _, dir := range []string{"lower1", "lower2", "upper", "work", "merged"} {
		if err := os.Mkdir(filepath.Join(td, dir), 0755); err != nil {
			return false, err
		}
	}

	upper := filepath.Join(td, "upper")
	work := filepath.Join(td, "work")
	lowers := fmt.Sprintf("%s:%s", filepath.Join(td, "lower2"), filepath.Join(td, "lower1"))
	options := []string{
		fmt.Sprintf("lowerdir=%s", lowers),
		fmt.Sprintf("upperdir=%s", upper),
		fmt.Sprintf("workdir=%s", work),
		"volatile",
	}

	if userxattr, err := overlayutils.NeedsUserXAttr(rootDir); err == nil && userxattr {
		options = append(options, "userxattr")
	}

	m := mount.Mount{
		Type:    "overlay",
		Source:  "overlay",
		Options: options,
	}
	dest := filepath.Join(td, "merged")
	if err := m.Mount(dest); err != nil {
		return false, err
	}
	if err := mount.UnmountAll(dest, 0); err != nil {
		slog.Debug("failed to unmount overlayfs volatile check", "err", err)
	}
	return true, nil
}
