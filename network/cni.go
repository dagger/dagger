package network

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/adrg/xdg"
	"github.com/jackpal/gateway"
	"github.com/sirupsen/logrus"
)

func InstallCNIConfig(name, subnet string) (string, error) {
	cniConfigPath, err := touchXDGFile("dagger/net/" + name + "/cni.conflist")
	if err != nil {
		return "", err
	}

	cni, err := cniConfig(name, subnet)
	if err != nil {
		return "", err
	}

	if err := os.WriteFile(cniConfigPath, cni, 0600); err != nil {
		return "", err
	}

	return cniConfigPath, nil
}

func cniConfig(name, subnet string) ([]byte, error) {
	bridgePlugin := map[string]any{
		"type":             "bridge",
		"bridge":           name + "0",
		"isDefaultGateway": true,
		"ipMasq":           true,
		"hairpinMode":      true,
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

	pidFile, err := xdg.RuntimeFile("dagger/net/" + name + "/dnsmasq.pid")
	if err != nil {
		return nil, err
	}

	hostsFile, err := xdg.RuntimeFile("dagger/net/" + name + "/hosts")
	if err != nil {
		return nil, err
	}

	return json.Marshal(map[string]any{
		"cniVersion": "0.4.0",
		"name":       name,
		"plugins": []any{
			bridgePlugin,
			map[string]any{
				"type": "firewall",
			},
			map[string]any{
				"type":       "dnsname",
				"domainName": name + ".local",
				"pidfile":    pidFile,
				"hosts":      hostsFile,
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
