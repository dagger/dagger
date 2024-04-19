//go:build linux || windows || freebsd
// +build linux windows freebsd

package main

import (
	"context"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	runhcsoptions "github.com/Microsoft/hcsshim/cmd/containerd-shim-runhcs-v1/options"
	ctd "github.com/containerd/containerd"
	"github.com/containerd/containerd/defaults"
	runtimeoptions "github.com/containerd/containerd/pkg/runtimeoptions/v1"
	"github.com/containerd/containerd/pkg/userns"
	"github.com/containerd/containerd/plugin"
	runcoptions "github.com/containerd/containerd/runtime/v2/runc/options"
	"github.com/moby/buildkit/cmd/buildkitd/config"
	"github.com/moby/buildkit/util/bklog"
	"github.com/moby/buildkit/util/network/cniprovider"
	"github.com/moby/buildkit/util/network/netproviders"
	"github.com/moby/buildkit/worker"
	"github.com/moby/buildkit/worker/base"
	"github.com/moby/buildkit/worker/containerd"
	"github.com/pelletier/go-toml"
	"github.com/pkg/errors"
	"github.com/urfave/cli"
	"golang.org/x/sync/semaphore"
)

const (
	defaultContainerdNamespace = "buildkit"

	// runtimeRunhcsV1 is the runtime type for runhcs.
	runtimeRunhcsV1 = "io.containerd.runhcs.v1"
)

func init() {
	defaultConf, _ := defaultConf()

	enabledValue := func(b *bool) string {
		if b == nil {
			return "auto"
		}
		return strconv.FormatBool(*b)
	}

	if defaultConf.Workers.Containerd.Address == "" {
		defaultConf.Workers.Containerd.Address = defaultContainerdAddress
	}

	if defaultConf.Workers.Containerd.Namespace == "" {
		defaultConf.Workers.Containerd.Namespace = defaultContainerdNamespace
	}

	if defaultConf.Workers.Containerd.Runtime.Name == "" {
		if runtime.GOOS == "freebsd" {
			// TODO: this can be removed once containerd/containerd#8964 is included
			defaultConf.Workers.Containerd.Runtime.Name = "wtf.sbk.runj.v1"
		} else {
			defaultConf.Workers.Containerd.Runtime.Name = defaults.DefaultRuntime
		}
	}

	flags := []cli.Flag{
		cli.StringFlag{
			Name:  "containerd-worker",
			Usage: "enable containerd workers (true/false/auto)",
			Value: enabledValue(defaultConf.Workers.Containerd.Enabled),
		},
		cli.StringFlag{
			Name:  "containerd-worker-addr",
			Usage: "containerd socket",
			Value: defaultConf.Workers.Containerd.Address,
		},
		cli.StringSliceFlag{
			Name:  "containerd-worker-labels",
			Usage: "user-specific annotation labels (com.example.foo=bar)",
		},
		// TODO: containerd-worker-platform should be replaced by ability
		// to set these from containerd configuration
		cli.StringSliceFlag{
			Name:   "containerd-worker-platform",
			Usage:  "override supported platforms for worker",
			Hidden: true,
		},
		cli.StringFlag{
			Name:   "containerd-worker-namespace",
			Usage:  "override containerd namespace",
			Value:  defaultConf.Workers.Containerd.Namespace,
			Hidden: true,
		},
		cli.StringFlag{
			Name:   "containerd-worker-runtime",
			Usage:  "override containerd runtime",
			Value:  defaultConf.Workers.Containerd.Runtime.Name,
			Hidden: true,
		},
		cli.StringFlag{
			Name:  "containerd-worker-net",
			Usage: "worker network type (auto, bridge, cni or host)",
			Value: defaultConf.Workers.Containerd.NetworkConfig.Mode,
		},
		cli.StringFlag{
			Name:  "containerd-cni-config-path",
			Usage: "path of cni config file",
			Value: defaultConf.Workers.Containerd.NetworkConfig.CNIConfigPath,
		},
		cli.StringFlag{
			Name:  "containerd-cni-binary-dir",
			Usage: "path of cni binary files",
			Value: defaultConf.Workers.Containerd.NetworkConfig.CNIBinaryPath,
		},
		cli.IntFlag{
			Name:  "containerd-cni-pool-size",
			Usage: "size of cni network namespace pool",
			Value: defaultConf.Workers.Containerd.NetworkConfig.CNIPoolSize,
		},
		cli.StringFlag{
			Name:  "containerd-worker-snapshotter",
			Usage: "snapshotter name to use",
			Value: ctd.DefaultSnapshotter,
		},
		cli.StringFlag{
			Name:  "containerd-worker-apparmor-profile",
			Usage: "set the name of the apparmor profile applied to containers",
		},
		cli.BoolFlag{
			Name:  "containerd-worker-selinux",
			Usage: "apply SELinux labels",
		},
	}
	n := "containerd-worker-rootless"
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

	if defaultConf.Workers.Containerd.GC == nil || *defaultConf.Workers.Containerd.GC {
		flags = append(flags, cli.BoolTFlag{
			Name:  "containerd-worker-gc",
			Usage: "Enable automatic garbage collection on worker",
		})
	} else {
		flags = append(flags, cli.BoolFlag{
			Name:  "containerd-worker-gc",
			Usage: "Enable automatic garbage collection on worker",
		})
	}
	flags = append(flags, cli.Int64Flag{
		Name:  "containerd-worker-gc-keepstorage",
		Usage: "Amount of storage GC keep locally (MB)",
		Value: func() int64 {
			keep := defaultConf.Workers.Containerd.GCKeepStorage.AsBytes(defaultConf.Root)
			if keep == 0 {
				keep = config.DetectDefaultGCCap().AsBytes(defaultConf.Root)
			}
			return keep / 1e6
		}(),
		Hidden: len(defaultConf.Workers.Containerd.GCPolicy) != 0,
	})

	registerWorkerInitializer(
		workerInitializer{
			fn: containerdWorkerInitializer,
			// 1 is less preferred than 0 (runcCtor)
			priority: 1,
		},
		flags...,
	)
	// TODO(AkihiroSuda): allow using multiple snapshotters. should be useful for some applications that does not work with the default overlay snapshotter. e.g. mysql (docker/for-linux#72)",
}

func applyContainerdFlags(c *cli.Context, cfg *config.Config) error {
	if cfg.Workers.Containerd.Address == "" {
		cfg.Workers.Containerd.Address = defaultContainerdAddress
	}

	if c.GlobalIsSet("containerd-worker") {
		boolOrAuto, err := parseBoolOrAuto(c.GlobalString("containerd-worker"))
		if err != nil {
			return err
		}
		cfg.Workers.Containerd.Enabled = boolOrAuto
	}

	if c.GlobalIsSet("rootless") || c.GlobalBool("rootless") {
		cfg.Workers.Containerd.Rootless = c.GlobalBool("rootless")
	}
	if c.GlobalIsSet("containerd-worker-rootless") {
		if !userns.RunningInUserNS() || os.Geteuid() > 0 {
			return errors.New("rootless mode requires to be executed as the mapped root in a user namespace; you may use RootlessKit for setting up the namespace")
		}
		cfg.Workers.Containerd.Rootless = c.GlobalBool("containerd-worker-rootless")
	}

	labels, err := attrMap(c.GlobalStringSlice("containerd-worker-labels"))
	if err != nil {
		return err
	}
	if cfg.Workers.Containerd.Labels == nil {
		cfg.Workers.Containerd.Labels = make(map[string]string)
	}
	for k, v := range labels {
		cfg.Workers.Containerd.Labels[k] = v
	}
	if c.GlobalIsSet("containerd-worker-addr") {
		cfg.Workers.Containerd.Address = c.GlobalString("containerd-worker-addr")
	}

	if platforms := c.GlobalStringSlice("containerd-worker-platform"); len(platforms) != 0 {
		cfg.Workers.Containerd.Platforms = platforms
	}

	if c.GlobalIsSet("containerd-worker-namespace") || cfg.Workers.Containerd.Namespace == "" {
		cfg.Workers.Containerd.Namespace = c.GlobalString("containerd-worker-namespace")
	}

	if c.GlobalIsSet("containerd-worker-runtime") || cfg.Workers.Containerd.Runtime.Name == "" {
		cfg.Workers.Containerd.Runtime = config.ContainerdRuntime{
			Name: c.GlobalString("containerd-worker-runtime"),
		}
	}

	if c.GlobalIsSet("containerd-worker-gc") {
		v := c.GlobalBool("containerd-worker-gc")
		cfg.Workers.Containerd.GC = &v
	}

	if c.GlobalIsSet("containerd-worker-gc-keepstorage") {
		cfg.Workers.Containerd.GCKeepStorage = config.DiskSpace{Bytes: c.GlobalInt64("containerd-worker-gc-keepstorage") * 1e6}
	}

	if c.GlobalIsSet("containerd-worker-net") {
		cfg.Workers.Containerd.NetworkConfig.Mode = c.GlobalString("containerd-worker-net")
	}
	if c.GlobalIsSet("containerd-cni-config-path") {
		cfg.Workers.Containerd.NetworkConfig.CNIConfigPath = c.GlobalString("containerd-cni-config-path")
	}
	if c.GlobalIsSet("containerd-cni-pool-size") {
		cfg.Workers.Containerd.NetworkConfig.CNIPoolSize = c.GlobalInt("containerd-cni-pool-size")
	}
	if c.GlobalIsSet("containerd-cni-binary-dir") {
		cfg.Workers.Containerd.NetworkConfig.CNIBinaryPath = c.GlobalString("containerd-cni-binary-dir")
	}
	if c.GlobalIsSet("containerd-worker-snapshotter") {
		cfg.Workers.Containerd.Snapshotter = c.GlobalString("containerd-worker-snapshotter")
	}
	if c.GlobalIsSet("containerd-worker-apparmor-profile") {
		cfg.Workers.Containerd.ApparmorProfile = c.GlobalString("containerd-worker-apparmor-profile")
	}
	if c.GlobalIsSet("containerd-worker-selinux") {
		cfg.Workers.Containerd.SELinux = c.GlobalBool("containerd-worker-selinux")
	}

	return nil
}

func containerdWorkerInitializer(c *cli.Context, common workerInitializerOpt) ([]worker.Worker, error) {
	if err := applyContainerdFlags(c, common.config); err != nil {
		return nil, err
	}

	cfg := common.config.Workers.Containerd

	if (cfg.Enabled == nil && !validContainerdSocket(cfg)) || (cfg.Enabled != nil && !*cfg.Enabled) {
		return nil, nil
	}

	if cfg.Rootless {
		bklog.L.Debugf("running in rootless mode")
		if common.config.Workers.Containerd.NetworkConfig.Mode == "auto" {
			common.config.Workers.Containerd.NetworkConfig.Mode = "host"
		}
	}

	dns := getDNSConfig(common.config.DNS)

	nc := netproviders.Opt{
		Mode: common.config.Workers.Containerd.NetworkConfig.Mode,
		CNI: cniprovider.Opt{
			Root:         common.config.Root,
			ConfigPath:   common.config.Workers.Containerd.CNIConfigPath,
			BinaryDir:    common.config.Workers.Containerd.CNIBinaryPath,
			PoolSize:     common.config.Workers.Containerd.CNIPoolSize,
			BridgeName:   common.config.Workers.Containerd.BridgeName,
			BridgeSubnet: common.config.Workers.Containerd.BridgeSubnet,
		},
	}

	var parallelismSem *semaphore.Weighted
	if cfg.MaxParallelism > 0 {
		parallelismSem = semaphore.NewWeighted(int64(cfg.MaxParallelism))
	}

	snapshotter := ctd.DefaultSnapshotter
	if cfg.Snapshotter != "" {
		snapshotter = cfg.Snapshotter
	}

	var runtime *containerd.RuntimeInfo
	if cfg.Runtime.Name != "" {
		opts := getRuntimeOptionsType(cfg.Runtime.Name)

		t, err := toml.TreeFromMap(cfg.Runtime.Options)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to parse runtime options config")
		}
		err = t.Unmarshal(opts)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to parse runtime options config")
		}

		runtime = &containerd.RuntimeInfo{
			Name:    cfg.Runtime.Name,
			Path:    cfg.Runtime.Path,
			Options: opts,
		}
	}
	opt, err := containerd.NewWorkerOpt(common.config.Root, cfg.Address, snapshotter, cfg.Namespace, cfg.Rootless, cfg.Labels, dns, nc, common.config.Workers.Containerd.ApparmorProfile, common.config.Workers.Containerd.SELinux, parallelismSem, common.traceSocket, runtime, ctd.WithTimeout(60*time.Second))
	if err != nil {
		return nil, err
	}
	opt.GCPolicy = getGCPolicy(cfg.GCConfig, common.config.Root)
	opt.BuildkitVersion = getBuildkitVersion()
	opt.RegistryHosts = resolverFunc(common.config)

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

func validContainerdSocket(cfg config.ContainerdConfig) bool {
	socket := cfg.Address
	if strings.HasPrefix(socket, "tcp://") {
		// FIXME(AkihiroSuda): prohibit tcp?
		return true
	}
	socketPath := strings.TrimPrefix(socket, socketScheme)
	if _, err := os.Stat(socketPath); errors.Is(err, os.ErrNotExist) {
		// FIXME(AkihiroSuda): add more conditions
		bklog.L.Warnf("skipping containerd worker, as %q does not exist", socketPath)
		return false
	}
	c, err := ctd.New(socketPath, ctd.WithDefaultNamespace(cfg.Namespace))
	if err != nil {
		bklog.L.Warnf("skipping containerd worker, as failed to connect client to %q: %v", socketPath, err)
		return false
	}
	if _, err := c.Server(context.Background()); err != nil {
		bklog.L.Warnf("skipping containerd worker, as failed to call introspection API on %q: %v", socketPath, err)
		return false
	}
	return true
}

// getRuntimeOptionsType gets empty runtime options by the runtime type name.
func getRuntimeOptionsType(t string) interface{} {
	switch t {
	case plugin.RuntimeRuncV2:
		return &runcoptions.Options{}
	case runtimeRunhcsV1:
		return &runhcsoptions.Options{}
	default:
		return &runtimeoptions.Options{}
	}
}
