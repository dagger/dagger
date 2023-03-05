// Copyright 2019 dnsname authors
// Copyright 2017 CNI authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// This is a post-setup plugin that establishes port forwarding - using iptables,
// from the host's network interface(s) to a pod's network interface.
//
// It is intended to be used as a chained CNI plugin, and determines the container
// IP from the previous result. If the result includes an IPv6 address, it will
// also be configured. (IPTables will not forward cross-family).
//
// This has one notable limitation: it does not perform any kind of reservation
// of the actual host port. If there is a service on the host, it will have all
// its traffic captured by the container. If another container also claims a given
// port, it will capture the traffic - it is last-write-wins.
package main

import (
	"encoding/json"
	"fmt"

	"github.com/containernetworking/cni/pkg/skel"
	"github.com/containernetworking/cni/pkg/types"
	current "github.com/containernetworking/cni/pkg/types/100"
	"github.com/containernetworking/cni/pkg/version"
	"github.com/gofrs/flock"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

func cmdAdd(args *skel.CmdArgs) error {
	netConf, result, podname, err := parseConfig(args.StdinData, args.Args)
	if err != nil {
		return errors.Wrap(err, "failed to parse config")
	}

	if netConf.PrevResult == nil {
		return errors.Errorf("must be called as chained plugin")
	}

	ips, err := getIPs(result)
	if err != nil {
		return err
	}

	lock := flock.New(netConf.Hosts)
	if err := lock.Lock(); err != nil {
		return err
	}
	defer func() {
		if err := lock.Unlock(); err != nil {
			logrus.Errorf("unable to release lock for %q: %v", netConf.Hosts, err)
		}
	}()

	aliases := netConf.RuntimeConfig.Aliases[netConf.Name]
	if err := appendToFile(netConf.Hosts, podname, aliases, ips); err != nil {
		return err
	}
	// Now we need to HUP
	if err := hup(netConf.Pidfile); err != nil {
		return err
	}
	nameservers, err := getInterfaceAddresses(result.Interfaces[0].Name)
	if err != nil {
		return err
	}
	// keep anything that was passed in already
	nameservers = append(nameservers, result.DNS.Nameservers...)
	result.DNS.Nameservers = nameservers
	// add dns search domain
	result.DNS.Search = append(result.DNS.Search, netConf.DomainName)
	// Pass through the previous result
	return types.PrintResult(result, netConf.CNIVersion)
}

// Do not return an error, otherwise cni will stop
// and not invoke the following plugins del command.
func cmdDel(args *skel.CmdArgs) error {
	netConf, result, podname, err := parseConfig(args.StdinData, args.Args)
	if err != nil {
		logrus.Error(errors.Wrap(err, "failed to parse config"))
		return nil
	} else if result == nil {
		return nil
	}

	lock := flock.New(netConf.Hosts)
	if err := lock.Lock(); err != nil {
		return err
	}
	defer func() {
		if err := lock.Unlock(); err != nil {
			logrus.Errorf("unable to release lock for %q: %v", netConf.Hosts, err)
		}
	}()

	if err := removeFromFile(netConf.Hosts, podname); err != nil {
		logrus.Error(err)
		return nil
	}

	// Now we need to HUP
	err = hup(netConf.Pidfile)
	if err != nil {
		logrus.Error(err)
	}
	return nil
}

func main() {
	skel.PluginMain(cmdAdd, cmdCheck, cmdDel, version.All, getVersion())
}

func cmdCheck(args *skel.CmdArgs) error {
	netConf, result, _, err := parseConfig(args.StdinData, args.Args)
	if err != nil {
		return errors.Wrap(err, "failed to parse config")
	}

	// Ensure we have previous result.
	if result == nil {
		return errors.Errorf("Required prevResult missing")
	}

	lock := flock.New(netConf.Hosts)
	if err := lock.Lock(); err != nil {
		return err
	}
	defer func() {
		if err := lock.Unlock(); err != nil {
			logrus.Errorf("unable to release lock for %q: %v", netConf.Hosts, err)
		}
	}()

	if _, err := getProcess(netConf.Pidfile); err != nil {
		return fmt.Errorf("dnsmasq instance not running: %w", err)
	}

	return nil
}

// stringInSlice is simple util to check for the presence of a string
// in a string slice
func stringInSlice(s string, slice []string) bool {
	for _, sl := range slice {
		if s == sl {
			return true
		}
	}
	return false
}

type podname struct {
	types.CommonArgs
	K8S_POD_NAME types.UnmarshallableString `json:"podname,omitempty"` //nolint:revive,stylecheck
}

// parseConfig parses the supplied configuration (and prevResult) from stdin.
func parseConfig(stdin []byte, args string) (*DNSNameConf, *current.Result, string, error) {
	conf := DNSNameConf{}
	if err := json.Unmarshal(stdin, &conf); err != nil {
		return nil, nil, "", errors.Wrap(err, "failed to parse network configuration")
	}

	// Parse previous result.
	var result *current.Result
	if conf.RawPrevResult != nil {
		var err error
		if err = version.ParsePrevResult(&conf.NetConf); err != nil {
			return nil, nil, "", errors.Wrap(err, "could not parse prevResult")
		}
		result, err = current.NewResultFromResult(conf.PrevResult)
		if err != nil {
			return nil, nil, "", errors.Wrap(err, "could not convert result to current version")
		}
	}
	e := podname{}
	if err := types.LoadArgs(args, &e); err != nil {
		return nil, nil, "", err
	}
	return &conf, result, string(e.K8S_POD_NAME), nil
}
