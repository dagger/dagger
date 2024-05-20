package server

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	snapshotsapi "github.com/containerd/containerd/api/services/snapshots/v1"
	"github.com/containerd/containerd/defaults"
	"github.com/containerd/containerd/pkg/dialer"
	"github.com/containerd/containerd/reference"
	"github.com/containerd/containerd/remotes/docker"
	ctdsnapshot "github.com/containerd/containerd/snapshots"
	"github.com/containerd/containerd/snapshots/native"
	"github.com/containerd/containerd/snapshots/overlay"
	"github.com/containerd/containerd/snapshots/overlay/overlayutils"
	snproxy "github.com/containerd/containerd/snapshots/proxy"
	fuseoverlayfs "github.com/containerd/fuse-overlayfs-snapshotter"
	sgzfs "github.com/containerd/stargz-snapshotter/fs"
	sgzconf "github.com/containerd/stargz-snapshotter/fs/config"
	sgzlayer "github.com/containerd/stargz-snapshotter/fs/layer"
	sgzsource "github.com/containerd/stargz-snapshotter/fs/source"
	remotesn "github.com/containerd/stargz-snapshotter/snapshot"
	"github.com/moby/buildkit/cmd/buildkitd/config"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/util/resolver"
	"github.com/pelletier/go-toml"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/credentials/insecure"
)

func newSnapshotter(rootDir string, cfg config.OCIConfig, sm *session.Manager, hosts docker.RegistryHosts) (ctdsnapshot.Snapshotter, string, error) {
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
		sn, snErr = overlay.NewSnapshotter(rootDir, overlay.AsynchronousRemove)
	case "fuse-overlayfs":
		// no Opt (AsynchronousRemove is untested for fuse-overlayfs)
		sn, snErr = fuseoverlayfs.NewSnapshotter(rootDir)
	case "stargz":
		sgzCfg := sgzconf.Config{}
		if cfg.StargzSnapshotterConfig != nil {
			// In order to keep the stargz Config type (and dependency) out of
			// the main BuildKit config, the main config Unmarshalls it into a
			// generic map[string]interface{}. Here we convert it back into TOML
			// tree, and unmarshal it to the actual type.
			t, err := toml.TreeFromMap(cfg.StargzSnapshotterConfig)
			if err != nil {
				return nil, "", fmt.Errorf("failed to parse stargz config: %w", err)
			}
			err = t.Unmarshal(&sgzCfg)
			if err != nil {
				return nil, "", fmt.Errorf("failed to unmarshal stargz config: %w", err)
			}
		}
		userxattr, err := overlayutils.NeedsUserXAttr(rootDir)
		if err != nil {
			logrus.WithError(err).Warnf("cannot detect whether \"userxattr\" option needs to be used, assuming to be %v", userxattr)
		}
		opq := sgzlayer.OverlayOpaqueTrusted
		if userxattr {
			opq = sgzlayer.OverlayOpaqueUser
		}
		fs, err := sgzfs.NewFilesystem(filepath.Join(rootDir, "stargz"),
			sgzCfg,
			// Source info based on the buildkit's registry config and session
			sgzfs.WithGetSources(sourceWithSession(hosts, sm)),
			sgzfs.WithMetricsLogLevel(logrus.DebugLevel),
			sgzfs.WithOverlayOpaqueType(opq),
		)
		if err != nil {
			return nil, "", fmt.Errorf("failed to create stargz filesystem: %w", err)
		}
		sn, snErr = remotesn.NewSnapshotter(context.Background(),
			filepath.Join(rootDir, "snapshotter"),
			fs, remotesn.AsynchronousRemove, remotesn.NoRestore)
	default:
		return nil, "", fmt.Errorf("unknown snapshotter %q", name)
	}
	if snErr != nil {
		return nil, "", fmt.Errorf("failed to create snapshotter %q: %w", name, snErr)
	}

	return sn, name, nil
}

const (
	// targetRefLabel is a label which contains image reference.
	targetRefLabel = "containerd.io/snapshot/remote/stargz.reference"

	// targetDigestLabel is a label which contains layer digest.
	targetDigestLabel = "containerd.io/snapshot/remote/stargz.digest"

	// targetImageLayersLabel is a label which contains layer digests contained in
	// the target image.
	targetImageLayersLabel = "containerd.io/snapshot/remote/stargz.layers"

	// targetSessionLabel is a label which contains session IDs usable for
	// authenticating the target snapshot.
	targetSessionLabel = "containerd.io/snapshot/remote/stargz.session"
)

// sourceWithSession returns a callback which implements a converter from labels to the
// typed snapshot source info. This callback is called every time the snapshotter resolves a
// snapshot. This callback returns configuration that is based on buildkitd's registry config
// and utilizes the session-based authorizer.
func sourceWithSession(hosts docker.RegistryHosts, sm *session.Manager) sgzsource.GetSources {
	return func(labels map[string]string) (src []sgzsource.Source, err error) {
		// labels contains multiple source candidates with unique IDs appended on each call
		// to the snapshotter API. So, first, get all these IDs
		var ids []string
		for k := range labels {
			if strings.HasPrefix(k, targetRefLabel+".") {
				ids = append(ids, strings.TrimPrefix(k, targetRefLabel+"."))
			}
		}

		// Parse all labels
		for _, id := range ids {
			// Parse session labels
			ref, ok := labels[targetRefLabel+"."+id]
			if !ok {
				continue
			}
			named, err := reference.Parse(ref)
			if err != nil {
				continue
			}
			var sids []string
			for i := 0; ; i++ {
				sidKey := targetSessionLabel + "." + fmt.Sprintf("%d", i) + "." + id
				sid, ok := labels[sidKey]
				if !ok {
					break
				}
				sids = append(sids, sid)
			}

			// Get source information based on labels and RegistryHosts containing
			// session-based authorizer.
			parse := sgzsource.FromDefaultLabels(func(ref reference.Spec) ([]docker.RegistryHost, error) {
				return resolver.DefaultPool.GetResolver(hosts, named.String(), "pull", sm, session.NewGroup(sids...)).
					HostsFunc(ref.Hostname())
			})
			if s, err := parse(map[string]string{
				targetRefLabel:         ref,
				targetDigestLabel:      labels[targetDigestLabel+"."+id],
				targetImageLayersLabel: labels[targetImageLayersLabel+"."+id],
			}); err == nil {
				src = append(src, s...)
			}
		}

		return src, nil
	}
}
