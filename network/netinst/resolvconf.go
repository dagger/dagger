//go:build linux
// +build linux

package netinst

import (
	"bufio"
	"fmt"
	"net/netip"
	"os"
	"strings"
	"syscall"
)

const resolv = "/etc/resolv.conf"
const systemdResolv = "/run/systemd/resolve/resolv.conf"

func InstallResolvconf(name, containerDNS string) error {
	containerDNSResolv := containerResolvPath(name)
	if err := createIfNeeded(containerDNSResolv); err != nil {
		return err
	}

	// create the resolv.conf for the container namespace by swapping out the
	// nameservers from the original, keeping any options and search domains
	if err := replaceNameservers(containerDNS, containerDNSResolv); err != nil {
		return fmt.Errorf("replace nameservers: %w", err)
	}

	if err := createIfNeeded(upstreamResolvPath); err != nil {
		return err
	}

	// preserve original resolv.conf at upstream path
	//
	// if resolv.conf is bind mounted, its source will be bind mounted here
	if err := syscall.Mount(resolv, upstreamResolvPath, "", syscall.MS_BIND, ""); err != nil {
		return fmt.Errorf("remount /etc/resolv.conf to upstream alias: %w", err)
	}

	// mount container resolv.conf over /etc/resolv.conf
	if err := syscall.Mount(containerDNSResolv, resolv, "", syscall.MS_BIND|syscall.MS_RDONLY, ""); err != nil {
		return fmt.Errorf("mount over /etc/resolv.conf: %w", err)
	}

	return nil
}

func replaceNameservers(containerDNS, containerDNSResolve string) error {
	srcPath := systemdResolv
	if _, err := os.Stat(srcPath); err != nil {
		srcPath = resolv
	}
	if _, err := os.Stat(upstreamResolvPath); err == nil && srcPath == resolv {
		srcPath = upstreamResolvPath
	}

	src, err := os.Open(srcPath)
	if err != nil {
		return nil
	}
	defer src.Close()

	dst, err := os.OpenFile(containerDNSResolve, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer dst.Close()

	fmt.Fprintln(dst, "# container ns resolver")

	srcScan := bufio.NewScanner(src)
	nameservers := make([]string, 0, 2)
	lines := make([]string, 0, 8)

	for srcScan.Scan() {
		line := srcScan.Text()
		if strings.HasPrefix(line, "nameserver") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			if keepNameserver(fields[1], containerDNS) {
				nameservers = append(nameservers, fields[1])
			}
			continue
		}

		lines = append(lines, line)
	}

	if len(nameservers) == 0 {
		nameservers = []string{"1.1.1.1", "8.8.8.8"}
	}

	for _, nameserver := range nameservers {
		fmt.Fprintln(dst, "nameserver", nameserver)
	}
	for _, line := range lines {
		fmt.Fprintln(dst, line)
	}

	return dst.Close()
}

func keepNameserver(nameserver, containerDNS string) bool {
	if nameserver == containerDNS {
		return false
	}

	addr, err := netip.ParseAddr(nameserver)
	if err != nil {
		return false
	}

	if addr.IsLoopback() || addr.IsUnspecified() || addr.IsLinkLocalUnicast() {
		return false
	}

	if addr.IsPrivate() {
		return false
	}

	return true
}
