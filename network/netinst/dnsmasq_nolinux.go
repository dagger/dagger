//go:build !linux
// +build !linux

package netinst

import (
	"context"
	"fmt"
)

func InstallDnsmasq(context.Context, string) error {
	return fmt.Errorf("dnsmasq installation is only supported on linux")
}
