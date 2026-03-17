package main

import (
	"os"
	"path/filepath"

	bkconfig "github.com/dagger/dagger/internal/buildkit/cmd/buildkitd/config"
	"github.com/dagger/dagger/internal/buildkit/util/appdefaults"
	"github.com/dagger/dagger/internal/buildkit/util/archutil"
	"github.com/moby/sys/userns"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/distconsts"
	"github.com/dagger/dagger/engine/server"
)

func defaultBuildkitConfigPath() string {
	if userns.RunningInUserNS() {
		return filepath.Join(appdefaults.UserConfigDir(), "buildkitd.toml")
	}
	return filepath.Join(appdefaults.ConfigDir, "buildkitd.toml")
}

func defaultBuildkitConfig() (bkconfig.Config, error) {
	cfg, err := bkconfig.LoadFile(defaultBuildkitConfigPath())
	if err != nil {
		var pe *os.PathError
		if !errors.As(err, &pe) {
			return bkconfig.Config{}, err
		}
		logrus.Warnf("failed to load default config: %v", err)
	}
	setDefaultBuildkitConfig(&cfg, nil)

	return cfg, nil
}

func setDefaultBuildkitConfig(cfg *bkconfig.Config, netConf *networkConfig) {
	if cfg.Root == "" {
		cfg.Root = distconsts.EngineDefaultStateDir
	}

	// always include default addresses
	cfg.GRPC.Address = append([]string{appdefaults.Address, engine.DefaultEngineSockAddr}, cfg.GRPC.Address...)

	isTrue := true
	cfg.Workers.OCI.Enabled = &isTrue

	if cfg.Workers.OCI.Binary == "" {
		cfg.Workers.OCI.Binary = distconsts.RuncPath
	}

	if cfg.DNS == nil {
		cfg.DNS = &bkconfig.DNSConfig{}
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
}

func setNetworkDefaults(cfg *bkconfig.NetworkConfig, cniConfigPath string) {
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
