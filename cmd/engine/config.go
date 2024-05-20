package main

import (
	"os"
	"path/filepath"

	"github.com/containerd/containerd/pkg/userns"
	"github.com/moby/buildkit/cmd/buildkitd/config"
	"github.com/moby/buildkit/util/appdefaults"
	"github.com/moby/buildkit/util/archutil"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"

	"github.com/dagger/dagger/engine/distconsts"
	"github.com/dagger/dagger/engine/server"
)

func defaultConfigPath() string {
	if userns.RunningInUserNS() {
		return filepath.Join(appdefaults.UserConfigDir(), "buildkitd.toml")
	}
	return filepath.Join(appdefaults.ConfigDir, "buildkitd.toml")
}

func defaultConf() (config.Config, error) {
	cfg, err := config.LoadFile(defaultConfigPath())
	if err != nil {
		var pe *os.PathError
		if !errors.As(err, &pe) {
			return config.Config{}, err
		}
		logrus.Warnf("failed to load default config: %v", err)
	}
	setDefaultConfig(&cfg, nil)

	return cfg, nil
}

func setDefaultConfig(cfg *config.Config, netConf *networkConfig) {
	orig := *cfg

	if cfg.Root == "" {
		cfg.Root = distconsts.EngineDefaultStateDir
	}

	if len(cfg.GRPC.Address) == 0 {
		cfg.GRPC.Address = []string{appdefaults.Address}
	}

	isTrue := true
	cfg.Workers.OCI.Enabled = &isTrue

	if cfg.Workers.OCI.Binary == "" {
		cfg.Workers.OCI.Binary = distconsts.RuncPath
	}

	if cfg.DNS == nil {
		cfg.DNS = &config.DNSConfig{}
	}

	if netConf != nil {
		// set dnsmasq as the default nameserver
		cfg.DNS.Nameservers = []string{netConf.Bridge.String()}

		if netConf.CNIConfigPath != "" {
			setNetworkDefaults(&cfg.Workers.OCI.NetworkConfig, netConf.CNIConfigPath)
		}
	}

	if cfg.Workers.OCI.Platforms == nil {
		cfg.Workers.OCI.Platforms = server.FormatPlatforms(archutil.SupportedPlatforms(false))
	}
	if cfg.Workers.Containerd.Platforms == nil {
		cfg.Workers.Containerd.Platforms = server.FormatPlatforms(archutil.SupportedPlatforms(false))
	}

	if userns.RunningInUserNS() {
		// if buildkitd is being executed as the mapped-root (not only EUID==0 but also $USER==root)
		// in a user namespace, we need to enable the rootless mode but
		// we don't want to honor $HOME for setting up default paths.
		if u := os.Getenv("USER"); u != "" && u != "root" {
			if orig.Root == "" {
				cfg.Root = appdefaults.UserRoot()
			}
			if len(orig.GRPC.Address) == 0 {
				cfg.GRPC.Address = []string{appdefaults.UserAddress()}
			}
			appdefaults.EnsureUserAddressDir()
		}
	}
}

func setNetworkDefaults(cfg *config.NetworkConfig, cniConfigPath string) {
	if cfg.Mode == "" {
		cfg.Mode = "cni"
	}
	if cfg.CNIConfigPath == "" {
		cfg.CNIConfigPath = cniConfigPath
	}
	if cfg.CNIBinaryPath == "" {
		cfg.CNIBinaryPath = appdefaults.DefaultCNIBinDir
	}
	if cfg.CNIPoolSize == 0 {
		cfg.CNIPoolSize = 16
	}
}
