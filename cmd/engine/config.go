package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/network"
	"github.com/jackpal/gateway"
	"github.com/moby/buildkit/cmd/buildkitd/config"
)

// engineDefaultStateDir is the directory that we map to a volume by default.
const engineDefaultStateDir = "/var/lib/dagger"

// daggerConfigPath is the path containing Dagger-specific configuration, which
// might be provided by the user.
const daggerConfigPath = "/etc/dagger"

// cniConfigPath is the path to Dagger's CNI configuration. It will be
// generated if one isn't provided.
var cniConfigPath = filepath.Join(daggerConfigPath, "cni.conflist")

// servicesDNSEnvName is the feature flag for enabling the services network
// stack.
const servicesDNSEnvName = "_EXPERIMENTAL_DAGGER_SERVICES_DNS"

func setDaggerDefaults(cfg *config.Config) error {
	if cfg.Root == "" {
		cfg.Root = engineDefaultStateDir
	}

	if os.Getenv(servicesDNSEnvName) != "" {
		// check if CNI config already exists, just so we can respect a
		// user-provided config
		if _, err := os.Stat(cniConfigPath); os.IsNotExist(err) {
			cni, err := cniConfig(network.Name, network.CIDR)
			if err != nil {
				return err
			}

			if err := os.MkdirAll(filepath.Dir(cniConfigPath), 0700); err != nil {
				return err
			}

			if err := os.WriteFile(cniConfigPath, cni, 0600); err != nil {
				return err
			}
		}

		setNetworkDefaults(&cfg.Workers.OCI.NetworkConfig)

		// we don't use containerd, but make it match anyway
		setNetworkDefaults(&cfg.Workers.Containerd.NetworkConfig)
	}

	return nil
}

func setNetworkDefaults(cfg *config.NetworkConfig) {
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

func cniConfig(name, subnet string) ([]byte, error) {
	ip, err := gateway.DiscoverInterface()
	if err != nil {
		return nil, err
	}

	networkIface, err := findIfaceWithIP(ip.String())
	if err != nil {
		return nil, err
	}

	return json.Marshal(map[string]any{
		"cniVersion": "0.4.0",
		"name":       name,
		"plugins": []any{
			map[string]any{
				"type":             "bridge",
				"bridge":           name + "0",
				"isDefaultGateway": true,
				"ipMasq":           true,
				"hairpinMode":      true,
				"mtu":              networkIface.MTU,
				"ipam": map[string]any{
					"type": "host-local",
					"ranges": []any{
						[]any{map[string]any{"subnet": subnet}},
					},
				},
			},
			map[string]any{
				"type": "firewall",
			},
			map[string]any{
				"type":       "dnsname",
				"domainName": network.DNSDomain,
				"capabilities": map[string]any{
					"aliases": true,
				},
			},
		},
	})
}

func findIfaceWithIP(ip string) (net.Interface, error) {
	networkIfaces, err := net.Interfaces()
	if err != nil {
		return net.Interface{}, err
	}

	for _, networkIface := range networkIfaces {
		addrs, err := networkIface.Addrs()
		if err != nil {
			return net.Interface{}, err
		}

		for _, address := range addrs {
			if strings.HasPrefix(address.String(), ip+"/") {
				return networkIface, nil
			}
		}
	}

	return net.Interface{}, fmt.Errorf("no interface found for address %s", ip)
}
