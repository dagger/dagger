package main

import (
	"errors"

	"github.com/containernetworking/cni/pkg/types"
)

var (
	// ErrBinaryNotFound means that the dnsmasq binary was not found
	ErrBinaryNotFound = errors.New("unable to locate dnsmasq in path")
	// ErrNoIPAddressFound means that CNI was unable to resolve an IP address in the CNI configuration
	ErrNoIPAddressFound = errors.New("no ip address was found in the network")
)

// DNSNameConf represents the cni config with the domain name attribute
type DNSNameConf struct {
	types.NetConf
	DomainName    string   `json:"domainName"`
	Hosts         string   `json:"hosts"`
	Pidfile       string   `json:"pidfile"`
	RuntimeConfig struct { // The capability arg
		Aliases map[string][]string `json:"aliases"`
	} `json:"runtimeConfig,omitempty"`
}
