//go:build linux
// +build linux

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	snapshotsapi "github.com/containerd/containerd/api/services/snapshots/v1"
	"github.com/containerd/containerd/defaults"
	"github.com/containerd/containerd/pkg/dialer"
	"github.com/containerd/containerd/pkg/userns"
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
	"github.com/moby/buildkit/executor/oci"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/util/bklog"
	"github.com/moby/buildkit/util/network/cniprovider"
	"github.com/moby/buildkit/util/network/netproviders"
	"github.com/moby/buildkit/util/resolver"
	"github.com/moby/buildkit/worker"
	"github.com/moby/buildkit/worker/base"
	"github.com/moby/buildkit/worker/runc"
	"github.com/pelletier/go-toml"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"
	"golang.org/x/sync/semaphore"
	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/credentials/insecure"
)

func init() {
	defaultConf, _ := defaultConf()

	enabledValue := func(b *bool) string {
		if b == nil {
			return "auto"
		}
		return strconv.FormatBool(*b)
	}

	if defaultConf.Workers.OCI.Snapshotter == "" {
		defaultConf.Workers.OCI.Snapshotter = "auto"
	}

	flags := []cli.Flag{
		cli.StringFlag{
			Name:  "oci-worker",
			Usage: "enable oci workers (true/false/auto)",
			Value: enabledValue(defaultConf.Workers.OCI.Enabled),
		},
		cli.StringSliceFlag{
			Name:  "oci-worker-labels",
			Usage: "user-specific annotation labels (com.example.foo=bar)",
		},
		cli.StringFlag{
			Name:  "oci-worker-snapshotter",
			Usage: "name of snapshotter (overlayfs, native, etc.)",
			Value: defaultConf.Workers.OCI.Snapshotter,
		},
		cli.StringFlag{
			Name:  "oci-worker-proxy-snapshotter-path",
			Usage: "address of proxy snapshotter socket (do not include 'unix://' prefix)",
		},
		cli.StringSliceFlag{
			Name:  "oci-worker-platform",
			Usage: "override supported platforms for worker",
		},
		cli.StringFlag{
			Name:  "oci-worker-net",
			Usage: "worker network type (auto, bridge, cni or host)",
			Value: defaultConf.Workers.OCI.NetworkConfig.Mode,
		},
		cli.StringFlag{
			Name:  "oci-cni-config-path",
			Usage: "path of cni config file",
			Value: defaultConf.Workers.OCI.NetworkConfig.CNIConfigPath,
		},
		cli.StringFlag{
			Name:  "oci-cni-binary-dir",
			Usage: "path of cni binary files",
			Value: defaultConf.Workers.OCI.NetworkConfig.CNIBinaryPath,
		},
		cli.IntFlag{
			Name:  "oci-cni-pool-size",
			Usage: "size of cni network namespace pool",
			Value: defaultConf.Workers.OCI.NetworkConfig.CNIPoolSize,
		},
		cli.StringFlag{
			Name:  "oci-worker-binary",
			Usage: "name of specified oci worker binary",
			Value: defaultConf.Workers.OCI.Binary,
		},
		cli.StringFlag{
			Name:  "oci-worker-apparmor-profile",
			Usage: "set the name of the apparmor profile applied to containers",
		},
		cli.BoolFlag{
			Name:  "oci-worker-selinux",
			Usage: "apply SELinux labels",
		},
	}
	n := "oci-worker-rootless"
	u := "enable rootless mode"
	if userns.RunningInUserNS() {
		flags = append(flags, cli.BoolTFlag{
			Name:  n,
			Usage: u,
		})
	} else {
		flags = append(flags, cli.BoolFlag{
			Name:  n,
			Usage: u,
		})
	}
	flags = append(flags, cli.BoolFlag{
		Name:  "oci-worker-no-process-sandbox",
		Usage: "use the host PID namespace and procfs (WARNING: allows build containers to kill (and potentially ptrace) an arbitrary process in the host namespace)",
	})
	if defaultConf.Workers.OCI.GC == nil || *defaultConf.Workers.OCI.GC {
		flags = append(flags, cli.BoolTFlag{
			Name:  "oci-worker-gc",
			Usage: "Enable automatic garbage collection on worker",
		})
	} else {
		flags = append(flags, cli.BoolFlag{
			Name:  "oci-worker-gc",
			Usage: "Enable automatic garbage collection on worker",
		})
	}
	flags = append(flags, cli.Int64Flag{
		Name:  "oci-worker-gc-keepstorage",
		Usage: "Amount of storage GC keep locally (MB)",
		Value: func() int64 {
			keep := defaultConf.Workers.OCI.GCKeepStorage.AsBytes(defaultConf.Root)
			if keep == 0 {
				keep = config.DetectDefaultGCCap().AsBytes(defaultConf.Root)
			}
			return keep / 1e6
		}(),
		Hidden: len(defaultConf.Workers.OCI.GCPolicy) != 0,
	})

	registerWorkerInitializer(
		workerInitializer{
			fn:       ociWorkerInitializer,
			priority: 0,
		},
		flags...,
	)
	// TODO: allow multiple oci runtimes
}

func applyOCIFlags(c *cli.Context, cfg *config.Config) error {
	if cfg.Workers.OCI.Snapshotter == "" {
		cfg.Workers.OCI.Snapshotter = "auto"
	}

	if c.GlobalIsSet("oci-worker") {
		boolOrAuto, err := parseBoolOrAuto(c.GlobalString("oci-worker"))
		if err != nil {
			return err
		}
		cfg.Workers.OCI.Enabled = boolOrAuto
	}

	labels, err := attrMap(c.GlobalStringSlice("oci-worker-labels"))
	if err != nil {
		return err
	}
	if cfg.Workers.OCI.Labels == nil {
		cfg.Workers.OCI.Labels = make(map[string]string)
	}
	for k, v := range labels {
		cfg.Workers.OCI.Labels[k] = v
	}
	if c.GlobalIsSet("oci-worker-snapshotter") {
		cfg.Workers.OCI.Snapshotter = c.GlobalString("oci-worker-snapshotter")
	}

	if c.GlobalIsSet("rootless") || c.GlobalBool("rootless") {
		cfg.Workers.OCI.Rootless = c.GlobalBool("rootless")
	}
	if c.GlobalIsSet("oci-worker-rootless") {
		if !userns.RunningInUserNS() || os.Geteuid() > 0 {
			return errors.New("rootless mode requires to be executed as the mapped root in a user namespace; you may use RootlessKit for setting up the namespace")
		}
		cfg.Workers.OCI.Rootless = c.GlobalBool("oci-worker-rootless")
	}
	if c.GlobalIsSet("oci-worker-no-process-sandbox") {
		cfg.Workers.OCI.NoProcessSandbox = c.GlobalBool("oci-worker-no-process-sandbox")
	}

	if platforms := c.GlobalStringSlice("oci-worker-platform"); len(platforms) != 0 {
		cfg.Workers.OCI.Platforms = platforms
	}

	if c.GlobalIsSet("oci-worker-gc") {
		v := c.GlobalBool("oci-worker-gc")
		cfg.Workers.OCI.GC = &v
	}

	if c.GlobalIsSet("oci-worker-gc-keepstorage") {
		cfg.Workers.OCI.GCKeepStorage = config.DiskSpace{Bytes: c.GlobalInt64("oci-worker-gc-keepstorage") * 1e6}
	}

	if c.GlobalIsSet("oci-worker-net") {
		cfg.Workers.OCI.NetworkConfig.Mode = c.GlobalString("oci-worker-net")
	}
	if c.GlobalIsSet("oci-cni-config-path") {
		cfg.Workers.OCI.NetworkConfig.CNIConfigPath = c.GlobalString("oci-cni-worker-path")
	}
	if c.GlobalIsSet("oci-cni-binary-dir") {
		cfg.Workers.OCI.NetworkConfig.CNIBinaryPath = c.GlobalString("oci-cni-binary-dir")
	}
	if c.GlobalIsSet("oci-cni-pool-size") {
		cfg.Workers.OCI.NetworkConfig.CNIPoolSize = c.GlobalInt("oci-cni-pool-size")
	}
	if c.GlobalIsSet("oci-worker-binary") {
		cfg.Workers.OCI.Binary = c.GlobalString("oci-worker-binary")
	}
	if c.GlobalIsSet("oci-worker-proxy-snapshotter-path") {
		cfg.Workers.OCI.ProxySnapshotterPath = c.GlobalString("oci-worker-proxy-snapshotter-path")
	}
	if c.GlobalIsSet("oci-worker-apparmor-profile") {
		cfg.Workers.OCI.ApparmorProfile = c.GlobalString("oci-worker-apparmor-profile")
	}
	if c.GlobalIsSet("oci-worker-selinux") {
		cfg.Workers.OCI.SELinux = c.GlobalBool("oci-worker-selinux")
	}

	return nil
}

func ociWorkerInitializer(c *cli.Context, common workerInitializerOpt) ([]worker.Worker, error) {
	if err := applyOCIFlags(c, common.config); err != nil {
		return nil, err
	}

	cfg := common.config.Workers.OCI

	if (cfg.Enabled == nil && !validOCIBinary()) || (cfg.Enabled != nil && !*cfg.Enabled) {
		return nil, nil
	}

	// TODO: this should never change the existing state dir
	idmapping, err := parseIdentityMapping(cfg.UserRemapUnsupported)
	if err != nil {
		return nil, err
	}

	hosts := resolverFunc(common.config)
	snFactory, err := snapshotterFactory(common.config.Root, cfg, common.sessionManager, hosts)
	if err != nil {
		return nil, err
	}

	if cfg.Rootless {
		bklog.L.Debugf("running in rootless mode")
		if common.config.Workers.OCI.NetworkConfig.Mode == "auto" {
			common.config.Workers.OCI.NetworkConfig.Mode = "host"
		}
	}

	processMode := oci.ProcessSandbox
	if cfg.NoProcessSandbox {
		bklog.L.Warn("NoProcessSandbox is enabled. Note that NoProcessSandbox allows build containers to kill (and potentially ptrace) an arbitrary process in the BuildKit host namespace. NoProcessSandbox should be enabled only when the BuildKit is running in a container as an unprivileged user.")
		if !cfg.Rootless {
			return nil, errors.New("can't enable NoProcessSandbox without Rootless")
		}
		processMode = oci.NoProcessSandbox
	}

	dns := getDNSConfig(common.config.DNS)

	nc := netproviders.Opt{
		Mode: common.config.Workers.OCI.NetworkConfig.Mode,
		CNI: cniprovider.Opt{
			Root:         common.config.Root,
			ConfigPath:   common.config.Workers.OCI.CNIConfigPath,
			BinaryDir:    common.config.Workers.OCI.CNIBinaryPath,
			PoolSize:     common.config.Workers.OCI.CNIPoolSize,
			BridgeName:   common.config.Workers.OCI.BridgeName,
			BridgeSubnet: common.config.Workers.OCI.BridgeSubnet,
		},
	}

	var parallelismSem *semaphore.Weighted
	if cfg.MaxParallelism > 0 {
		parallelismSem = semaphore.NewWeighted(int64(cfg.MaxParallelism))
	}

	opt, err := runc.NewWorkerOpt(common.config.Root, snFactory, cfg.Rootless, processMode, cfg.Labels, idmapping, nc, dns, cfg.Binary, cfg.ApparmorProfile, cfg.SELinux, parallelismSem, common.traceSocket, cfg.DefaultCgroupParent)
	if err != nil {
		return nil, err
	}
	opt.GCPolicy = getGCPolicy(cfg.GCConfig, common.config.Root)
	opt.BuildkitVersion = getBuildkitVersion()
	opt.RegistryHosts = hosts

	if platformsStr := cfg.Platforms; len(platformsStr) != 0 {
		platforms, err := parsePlatforms(platformsStr)
		if err != nil {
			return nil, errors.Wrap(err, "invalid platforms")
		}
		opt.Platforms = platforms
	}
	w, err := base.NewWorker(context.TODO(), opt)
	if err != nil {
		return nil, err
	}
	return []worker.Worker{w}, nil
}

func snapshotterFactory(commonRoot string, cfg config.OCIConfig, sm *session.Manager, hosts docker.RegistryHosts) (runc.SnapshotterFactory, error) {
	var (
		name    = cfg.Snapshotter
		address = cfg.ProxySnapshotterPath
	)
	if address != "" {
		snFactory := runc.SnapshotterFactory{
			Name: name,
		}
		if _, err := os.Stat(address); os.IsNotExist(err) {
			return snFactory, errors.Wrapf(err, "snapshotter doesn't exist on %q (Do not include 'unix://' prefix)", address)
		}
		snFactory.New = func(root string) (ctdsnapshot.Snapshotter, error) {
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
			conn, err := grpc.Dial(dialer.DialAddress(address), gopts...)
			if err != nil {
				return nil, errors.Wrapf(err, "failed to dial %q", address)
			}
			return snproxy.NewSnapshotter(snapshotsapi.NewSnapshotsClient(conn), name), nil
		}
		return snFactory, nil
	}

	if name == "auto" {
		if err := overlayutils.Supported(commonRoot); err == nil {
			name = "overlayfs"
		} else {
			bklog.L.Debugf("auto snapshotter: overlayfs is not available for %s, trying fuse-overlayfs: %v", commonRoot, err)
			if err2 := fuseoverlayfs.Supported(commonRoot); err2 == nil {
				name = "fuse-overlayfs"
			} else {
				bklog.L.Debugf("auto snapshotter: fuse-overlayfs is not available for %s, falling back to native: %v", commonRoot, err2)
				name = "native"
			}
		}
		bklog.L.Infof("auto snapshotter: using %s", name)
	}

	snFactory := runc.SnapshotterFactory{
		Name: name,
	}
	switch name {
	case "native":
		snFactory.New = native.NewSnapshotter
	case "overlayfs": // not "overlay", for consistency with containerd snapshotter plugin ID.
		snFactory.New = func(root string) (ctdsnapshot.Snapshotter, error) {
			return overlay.NewSnapshotter(root, overlay.AsynchronousRemove)
		}
	case "fuse-overlayfs":
		snFactory.New = func(root string) (ctdsnapshot.Snapshotter, error) {
			// no Opt (AsynchronousRemove is untested for fuse-overlayfs)
			return fuseoverlayfs.NewSnapshotter(root)
		}
	case "stargz":
		sgzCfg := sgzconf.Config{}
		if cfg.StargzSnapshotterConfig != nil {
			// In order to keep the stargz Config type (and dependency) out of
			// the main BuildKit config, the main config Unmarshalls it into a
			// generic map[string]interface{}. Here we convert it back into TOML
			// tree, and unmarshal it to the actual type.
			t, err := toml.TreeFromMap(cfg.StargzSnapshotterConfig)
			if err != nil {
				return snFactory, errors.Wrapf(err, "failed to parse stargz config")
			}
			err = t.Unmarshal(&sgzCfg)
			if err != nil {
				return snFactory, errors.Wrapf(err, "failed to parse stargz config")
			}
		}
		snFactory.New = func(root string) (ctdsnapshot.Snapshotter, error) {
			userxattr, err := overlayutils.NeedsUserXAttr(root)
			if err != nil {
				bklog.L.WithError(err).Warnf("cannot detect whether \"userxattr\" option needs to be used, assuming to be %v", userxattr)
			}
			opq := sgzlayer.OverlayOpaqueTrusted
			if userxattr {
				opq = sgzlayer.OverlayOpaqueUser
			}
			fs, err := sgzfs.NewFilesystem(filepath.Join(root, "stargz"),
				sgzCfg,
				// Source info based on the buildkit's registry config and session
				sgzfs.WithGetSources(sourceWithSession(hosts, sm)),
				sgzfs.WithMetricsLogLevel(logrus.DebugLevel),
				sgzfs.WithOverlayOpaqueType(opq),
			)
			if err != nil {
				return nil, err
			}
			return remotesn.NewSnapshotter(context.Background(),
				filepath.Join(root, "snapshotter"),
				fs, remotesn.AsynchronousRemove, remotesn.NoRestore)
		}
	default:
		return snFactory, errors.Errorf("unknown snapshotter name: %q", name)
	}
	return snFactory, nil
}

func validOCIBinary() bool {
	_, err := exec.LookPath("runc")
	_, err1 := exec.LookPath("buildkit-runc")
	if err != nil && err1 != nil {
		bklog.L.Warnf("skipping oci worker, as runc does not exist")
		return false
	}
	return true
}

const (
	// targetRefLabel is a label which contains image reference.
	targetRefLabel = "containerd.io/snapshot/remote/stargz.reference"

	// targetDigestLabel is a label which contains layer digest.
	targetDigestLabel = "containerd.io/snapshot/remote/stargz.digest"

	// targetImageLayersLabel is a label which contains layer digests contained in
	// the target image.
	targetImageLayersLabel = "containerd.io/snapshot/remote/stargz.layers"

	// targetSessionLabel is a labeld which contains session IDs usable for
	// authenticating the target snapshot.
	targetSessionLabel = "containerd.io/snapshot/remote/stargz.session"
)

// sourceWithSession returns a callback which implements a converter from labels to the
// typed snapshot source info. This callback is called everytime the snapshotter resolves a
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
