//go:build !windows
// +build !windows

package gitdns

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/dagger/dagger/engine/session/networks"
	"github.com/moby/sys/mount"
	"github.com/pkg/errors"
	"golang.org/x/sys/unix"
)

func runWithStandardUmaskAndNetOverride(ctx context.Context, cmd *exec.Cmd, hosts, resolv string) error {
	errCh := make(chan error)

	go func() {
		defer close(errCh)
		runtime.LockOSThread()

		if err := unshareAndRun(ctx, cmd, hosts, resolv); err != nil {
			errCh <- err
		}
	}()

	return <-errCh
}

// unshareAndRun needs to be called in a locked thread.
func unshareAndRun(ctx context.Context, cmd *exec.Cmd, hosts, resolv string) error {
	if err := syscall.Unshare(syscall.CLONE_FS | syscall.CLONE_NEWNS); err != nil {
		return err
	}
	syscall.Umask(0022)
	if err := overrideNetworkConfig(hosts, resolv); err != nil {
		return errors.Wrapf(err, "failed to override network config")
	}
	return runProcessGroup(ctx, cmd)
}

func runProcessGroup(ctx context.Context, cmd *exec.Cmd) error {
	cmd.SysProcAttr = &unix.SysProcAttr{
		Setpgid:   true,
		Pdeathsig: unix.SIGTERM,
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	waitDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = unix.Kill(-cmd.Process.Pid, unix.SIGTERM)
			go func() {
				select {
				case <-waitDone:
				case <-time.After(10 * time.Second):
					_ = unix.Kill(-cmd.Process.Pid, unix.SIGKILL)
				}
			}()
		case <-waitDone:
		}
	}()
	err := cmd.Wait()
	close(waitDone)
	return err
}

func overrideNetworkConfig(hostsOverride, resolvOverride string) error {
	if hostsOverride != "" {
		if err := mount.Mount(hostsOverride, "/etc/hosts", "", "bind"); err != nil {
			return errors.Wrap(err, "mount hosts override")
		}
	}

	if resolvOverride != "" {
		if err := syscall.Mount(resolvOverride, "/etc/resolv.conf", "", syscall.MS_BIND, ""); err != nil {
			return errors.Wrap(err, "mount resolv override")
		}
	}

	return nil
}

func (s *gitCLI) initConfig(netConf *networks.Config) error {
	if netConf == nil {
		return nil
	}

	if len(netConf.IpHosts) > 0 {
		if err := s.generateHosts(netConf.IpHosts); err != nil {
			return err
		}
	}

	if netConf.Dns != nil {
		if err := s.generateResolv(netConf.Dns); err != nil {
			return err
		}
	}

	return nil
}

func (s *gitCLI) generateHosts(extraHosts []*networks.IPHosts) error {
	src, err := os.Open("/etc/hosts")
	if err != nil {
		return errors.Wrap(err, "read current hosts")
	}
	defer src.Close()

	override, err := os.CreateTemp("", "buildkit-git-extra-hosts")
	if err != nil {
		return errors.Wrap(err, "create hosts override")
	}
	defer override.Close()

	s.hostsPath = override.Name()

	if err := mergeHosts(override, src, extraHosts); err != nil {
		return err
	}

	return override.Close()
}

func (s *gitCLI) generateResolv(dns *networks.DNSConfig) error {
	src, err := os.Open("/etc/resolv.conf")
	if err != nil {
		return err
	}
	defer src.Close()

	override, err := os.CreateTemp("", "buildkit-git-resolv")
	if err != nil {
		return errors.Wrap(err, "create hosts override")
	}

	s.resolvPath = override.Name()

	defer override.Close()

	if err := mergeResolv(override, src, dns); err != nil {
		return err
	}

	return nil
}

func mergeHosts(override *os.File, currentHosts io.Reader, extraHosts []*networks.IPHosts) error {
	if _, err := io.Copy(override, currentHosts); err != nil {
		return errors.Wrap(err, "write current hosts")
	}

	if _, err := fmt.Fprintln(override); err != nil {
		return errors.Wrap(err, "write newline")
	}

	for _, host := range extraHosts {
		hosts := strings.Join(host.Hosts, " ")
		if _, err := fmt.Fprintf(override, "%s\t%s\n", host.Ip, hosts); err != nil {
			return errors.Wrap(err, "write extra host")
		}
	}

	if err := override.Close(); err != nil {
		return errors.Wrap(err, "close hosts override")
	}

	return nil
}

func mergeResolv(dst *os.File, src io.Reader, dns *networks.DNSConfig) error {
	srcScan := bufio.NewScanner(src)

	var replacedSearch bool
	var replacedOptions bool

	for _, ns := range dns.Nameservers {
		fmt.Fprintln(dst, "nameserver", ns)
	}

	for srcScan.Scan() {
		switch {
		case strings.HasPrefix(srcScan.Text(), "search"):
			oldDomains := strings.Fields(srcScan.Text())[1:]
			newDomains := append([]string{}, dns.SearchDomains...)
			newDomains = append(newDomains, oldDomains...)
			fmt.Fprintln(dst, "search", strings.Join(newDomains, " "))
			replacedSearch = true
		case strings.HasPrefix(srcScan.Text(), "options"):
			oldOptions := strings.Fields(srcScan.Text())[1:]
			newOptions := append([]string{}, dns.Options...)
			newOptions = append(newOptions, oldOptions...)
			fmt.Fprintln(dst, "options", strings.Join(newOptions, " "))
			replacedOptions = true
		case strings.HasPrefix(srcScan.Text(), "nameserver"):
			if len(dns.Nameservers) == 0 {
				// preserve existing nameservers
				fmt.Fprintln(dst, srcScan.Text())
			}
		default:
			fmt.Fprintln(dst, srcScan.Text())
		}
	}

	if !replacedSearch {
		fmt.Fprintln(dst, "search", strings.Join(dns.SearchDomains, " "))
	}

	if !replacedOptions {
		fmt.Fprintln(dst, "options", strings.Join(dns.Options, " "))
	}

	return nil
}
