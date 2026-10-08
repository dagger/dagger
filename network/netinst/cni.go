package netinst

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackpal/gateway"
	"github.com/sirupsen/logrus"
)

// InstallCNIConfig writes the engine's CNI config. firewallPlugin keeps the
// CNI firewall plugin, for networks firewalld manages; otherwise
// InstallSubnetRules accepts forwarded traffic for the whole subnet.
func InstallCNIConfig(ctx context.Context, name, subnet string, firewallPlugin bool) (string, error) {
	cni, err := cniConfig(name, subnet, firewallPlugin)
	if err != nil {
		return "", err
	}

	cniConfigPath := cniConfPath(name)

	if err := os.MkdirAll(filepath.Dir(cniConfigPath), 0700); err != nil {
		return "", err
	}

	if err := os.WriteFile(cniConfigPath, cni, 0600); err != nil {
		return "", err
	}

	return cniConfigPath, nil
}

// detectIPMasqBackend determines whether to use iptables or nftables for IP masquerading.
// On kernels without CONFIG_NETFILTER_XTABLES_LEGACY (6.17+), legacy iptables fails.
// Returns "nftables" if legacy xtables is unavailable, empty string to use default.
func detectIPMasqBackend() string {
	if !legacyXtablesAvailable() {
		return "nftables"
	}

	return "iptables"
}

func cniConfig(name, subnet string, firewallPlugin bool) ([]byte, error) {
	bridgePlugin := map[string]any{
		"type":             "bridge",
		"bridge":           name + "0",
		"isDefaultGateway": true,
		// Masquerading is set up once for the whole subnet; see InstallSubnetRules.
		"ipMasq":      false,
		"hairpinMode": true,
		"ipam": map[string]any{
			"type": "host-local",
			"ranges": []any{
				[]any{map[string]any{"subnet": subnet}},
			},
		},
	}

	if ip, err := gateway.DiscoverInterface(); err == nil {
		if iface, err := findIfaceWithIP(ip.String()); err == nil {
			logrus.Infof("detected mtu %d via interface %s", iface.MTU, iface.Name)
			bridgePlugin["mtu"] = iface.MTU
		} else {
			logrus.Warnf("could not determine mtu: %s", err)
		}
	} else {
		logrus.Warnf("could not detect mtu: %s", err)
	}

	plugins := []any{bridgePlugin}
	if firewallPlugin {
		plugins = append(plugins, map[string]any{
			"type": "firewall",
		})
	}
	return json.Marshal(map[string]any{
		"cniVersion": "0.4.0",
		"name":       name,
		"plugins": append(plugins,
			map[string]any{
				"type":       "dnsname",
				"domainName": name + ".local",
				"pidfile":    pidfilePath(name),
				"hosts":      hostsPath(name),
				"lockfile":   lockfilePath(name),
				"capabilities": map[string]any{
					"aliases": true,
				},
			},
		),
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
