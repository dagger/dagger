package network

import "fmt"

// Location of the CNI configuration.
func cniConfPath(name string) string {
	return fmt.Sprintf("/var/run/containers/cni/%s/cni.conflist", name)
}
