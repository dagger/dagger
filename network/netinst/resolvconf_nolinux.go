//go:build !linux
// +build !linux

package netinst

import "fmt"

func InstallResolvconf(string, string) error {
	return fmt.Errorf("resolv.conf installation is only supported on linux")
}
