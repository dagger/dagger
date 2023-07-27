package networks

import (
	context "context"
	strings "strings"

	"github.com/moby/buildkit/session"
)

func (config *Config) ExtraHosts() string {
	out := ""
	for _, ipHost := range config.IpHosts {
		out += ipHost.Ip + " " + strings.Join(ipHost.Hosts, " ") + "\n"
	}
	return out
}

func LoadConfig(ctx context.Context, c session.Caller, id string) (*Config, error) {
	nw, err := NewNetworksClient(c.Conn()).GetNetwork(ctx, &GetNetworkRequest{ID: id})
	if err != nil {
		return nil, err
	}

	return nw.Config, nil
}

func MergeConfig(ctx context.Context, c session.Caller, base *Config, id string) (*Config, error) {
	custom, err := LoadConfig(ctx, c, id)
	if err != nil {
		return nil, err
	}

	if custom == nil {
		return base, nil
	}

	cp := *base

	cp.IpHosts = append(custom.IpHosts, cp.IpHosts...)

	dns := custom.Dns
	if dns != nil {
		var dnsCp DNSConfig
		if cp.Dns != nil {
			dnsCp = *cp.Dns
		}

		cp.Dns = &dnsCp

		if len(dns.Nameservers) > 0 {
			cp.Dns.Nameservers = dns.Nameservers
		}

		if len(dns.Options) > 0 {
			cp.Dns.Options = dns.Options
		}

		if len(dns.SearchDomains) > 0 {
			cp.Dns.SearchDomains = dns.SearchDomains
		}
	}

	return &cp, nil
}
