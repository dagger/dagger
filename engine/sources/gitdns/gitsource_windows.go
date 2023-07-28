//go:build windows
// +build windows

package gitdns

import (
	"context"
	"errors"
	"github.com/dagger/dagger/engine/session/networks"
	"os/exec"
)

func runWithStandardUmaskAndNetOverride(ctx context.Context, cmd *exec.Cmd, hosts, resolv string) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	waitDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			cmd.Process.Kill()
		case <-waitDone:
		}
	}()
	return cmd.Wait()
}

func (s *gitCLI) initConfig(dnsConf *oci.DNSConfig) error {
	if dnsConf == nil {
		return nil
	}

	return errors.New("overriding network config is not supported on Windows")
}
