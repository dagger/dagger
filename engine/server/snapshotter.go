package server

import (
	"fmt"
	"os"
	"time"

	snapshotsapi "github.com/containerd/containerd/api/services/snapshots/v1"
	ctdsnapshot "github.com/containerd/containerd/v2/core/snapshots"
	snproxy "github.com/containerd/containerd/v2/core/snapshots/proxy"
	"github.com/containerd/containerd/v2/core/snapshots/storage"
	"github.com/containerd/containerd/v2/defaults"
	"github.com/containerd/containerd/v2/pkg/dialer"
	"github.com/containerd/containerd/v2/plugins/snapshots/native"
	"github.com/containerd/containerd/v2/plugins/snapshots/overlay"
	"github.com/containerd/containerd/v2/plugins/snapshots/overlay/overlayutils"
	fuseoverlayfs "github.com/containerd/fuse-overlayfs-snapshotter/v2"
	bkconfig "github.com/dagger/dagger/internal/buildkit/cmd/buildkitd/config"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/credentials/insecure"
)

func newSnapshotter(
	rootDir string,
	cfg bkconfig.OCIConfig,
	mdStore *storage.MetaStore,
) (ctdsnapshot.Snapshotter, string, error) {
	var (
		name    = cfg.Snapshotter
		address = cfg.ProxySnapshotterPath
	)
	if address != "" {
		if _, err := os.Stat(address); os.IsNotExist(err) {
			return nil, "", fmt.Errorf("snapshotter doesn't exist on %q (Do not include 'unix://' prefix): %w", address, err)
		}
		backoffConfig := backoff.DefaultConfig
		backoffConfig.MaxDelay = 3 * time.Second
		connParams := grpc.ConnectParams{
			Backoff: backoffConfig,
		}
		gopts := []grpc.DialOption{
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithConnectParams(connParams),
			grpc.WithContextDialer(dialer.ContextDialer),
			grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(defaults.DefaultMaxRecvMsgSize)),
			grpc.WithDefaultCallOptions(grpc.MaxCallSendMsgSize(defaults.DefaultMaxSendMsgSize)),
		}
		conn, err := grpc.NewClient(dialer.DialAddress(address), gopts...)
		if err != nil {
			return nil, "", fmt.Errorf("failed to dial %q: %w", address, err)
		}
		return snproxy.NewSnapshotter(snapshotsapi.NewSnapshotsClient(conn), name), name, nil
	}

	if name == "" {
		if err := overlayutils.Supported(rootDir); err == nil {
			name = "overlayfs"
		} else {
			logrus.Debugf("auto snapshotter: overlayfs is not available for %s, trying fuse-overlayfs: %v", rootDir, err)
			if err2 := fuseoverlayfs.Supported(rootDir); err2 == nil {
				name = "fuse-overlayfs"
			} else {
				logrus.Debugf("auto snapshotter: fuse-overlayfs is not available for %s, falling back to native: %v", rootDir, err2)
				name = "native"
			}
		}
		logrus.Infof("auto snapshotter: using %s", name)
	}

	var sn ctdsnapshot.Snapshotter
	var snErr error
	switch name {
	case "native":
		sn, snErr = native.NewSnapshotter(rootDir)
	case "overlayfs": // not "overlay", for consistency with containerd snapshotter plugin ID.
		sn, snErr = overlay.NewSnapshotter(rootDir, overlay.AsynchronousRemove, overlay.WithMetaStore(mdStore))
	case "fuse-overlayfs":
		// no Opt (AsynchronousRemove is untested for fuse-overlayfs)
		sn, snErr = fuseoverlayfs.NewSnapshotter(rootDir)
	default:
		return nil, "", fmt.Errorf("unknown snapshotter %q", name)
	}
	if snErr != nil {
		return nil, "", fmt.Errorf("failed to create snapshotter %q: %w", name, snErr)
	}

	return sn, name, nil
}
