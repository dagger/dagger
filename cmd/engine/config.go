package main

import (
	"github.com/moby/buildkit/cmd/buildkitd/config"

	"github.com/dagger/dagger/engine/distconsts"
)

func setDaggerDefaults(cfg *config.Config, netConf *networkConfig) {
	if cfg.Root == "" {
		cfg.Root = distconsts.EngineDefaultStateDir
	}

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

			// we don't use containerd, but make it match anyway
			setNetworkDefaults(&cfg.Workers.Containerd.NetworkConfig, netConf.CNIConfigPath)
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
	if cfg.CNIPoolSize == 0 {
		cfg.CNIPoolSize = 16
	}
}
