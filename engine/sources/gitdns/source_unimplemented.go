//go:build windows || darwin

package gitdns

import (
	"context"
	"os/exec"

	"github.com/moby/buildkit/executor/oci"
)

func runWithStandardUmaskAndNetOverride(ctx context.Context, cmd *exec.Cmd, hosts, resolv string) error {
	panic("only implemented on linux")
}

func (cli *gitCLI) initConfig(dnsConf *oci.DNSConfig) error {
	panic("only implemented on linux")
}
