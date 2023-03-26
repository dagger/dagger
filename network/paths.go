package network

import "fmt"

// Location to store the unmodified original resolv.conf
const upstreamResolvPath = "/etc/dnsmasq-resolv.conf"

// Location of the dnsmasq.conf for the named network.
//
// NOTE: this path is chosen specifically to be compatible with the default
// AppArmor profile for /usr/sbin/dnsmasq.
func dnsmasqConfPath(name string) string {
	return fmt.Sprintf("/var/run/containers/cni/dnsname/%s/dnsmasq.conf", name)
}

// Location of the file which will store hostname to container IP mappings for
// the named network.
//
// NOTE: this path is chosen specifically to be compatible with the default
// AppArmor profile for /usr/sbin/dnsmasq.
func hostsPath(name string) string {
	return fmt.Sprintf("/var/run/containers/cni/dnsname/%s/addnhosts", name)
}

// Location of the dnsmasq pidfile for the named network.
//
// NOTE: this path is chosen specifically to be compatible with the default
// AppArmor profile for /usr/sbin/dnsmasq.
func pidfilePath(name string) string {
	return fmt.Sprintf("/var/run/containers/cni/dnsname/%s/pidfile", name)
}

// Location to store the resolv.conf that will be remounted to /etc/resolv.conf.
//
// This resolv.conf will contain only the dnsmasq nameserver, plus any search
// domains or options present in the upstream resolv.conf.
//
// NOTE: this is only placed beside the other paths for convenience; dnsmasq
// doesn't try to read it.
func containerResolvPath(name string) string {
	return fmt.Sprintf("/var/run/containers/cni/dnsname/%s/resolv.conf", name)
}

// Location of the CNI configuration which bundles our dnsname plugin.
//
// NOTE: this is only placed beside the other paths for convenience; dnsmasq
// doesn't try to read it.
func cniConfPath(name string) string {
	return fmt.Sprintf("/var/run/containers/cni/dnsname/%s/cni.conflist", name)
}
