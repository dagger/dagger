package main

import (
	"github.com/moby/buildkit/cmd/buildkitd/config"
)

// engineDefaultStateDir is the directory that we map to a volume by default.
const engineDefaultStateDir = "/var/lib/dagger"

// engineDefaultShimBin is the path to the shim binary we use as our oci runtime.
const engineDefaultShimBin = "/usr/local/bin/dagger-shim"

// servicesDNSEnvName is the feature flag for enabling the services network
// stack.
const servicesDNSEnvName = "_EXPERIMENTAL_DAGGER_SERVICES_DNS"

func setDaggerDefaults(cfg *config.Config, cniConfigPath string) error {
	if cfg.Root == "" {
		cfg.Root = engineDefaultStateDir
	}

	if cfg.Workers.OCI.Binary == "" {
		cfg.Workers.OCI.Binary = engineDefaultShimBin
	}

	if cniConfigPath != "" {
		setNetworkDefaults(&cfg.Workers.OCI.NetworkConfig, cniConfigPath)

		// we don't use containerd, but make it match anyway
		setNetworkDefaults(&cfg.Workers.Containerd.NetworkConfig, cniConfigPath)
	}

	return nil
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
