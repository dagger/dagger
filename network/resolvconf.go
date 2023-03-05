package network

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
)

const resolv = "/etc/resolv.conf"

func InstallResolvconf(name, containerDNS string) error {
	containerDNSResolv, err := touchXDGFile("dagger/net/" + name + "/resolv.conf")
	if err != nil {
		return err
	}

	// create the resolv.conf for the container namespace by swapping out the
	// nameservers from the original, keeping any options and search domains
	if err := replaceNameservers(containerDNS, containerDNSResolv); err != nil {
		return err
	}

	upstreamResolv, err := touchXDGFile("dagger/net/" + name + "/resolv.conf.upstream")
	if err != nil {
		return err
	}

	// preserve original resolv.conf at upstream path
	//
	// if resolv.conf is bind mounted, its source will be bind mounted here
	if err := syscall.Mount(resolv, upstreamResolv, "", syscall.MS_BIND, ""); err != nil {
		return err
	}

	// unmount target resolv.conf so we can replace it
	if err := syscall.Unmount(resolv, 0); err != nil && !errors.Is(err, syscall.EINVAL) {
		return err
	}

	// mount container resolv.conf over /etc/resolv.conf
	if err := syscall.Mount(containerDNSResolv, resolv, "", syscall.MS_BIND, ""); err != nil {
		return err
	}

	return nil
}

func replaceNameservers(containerDNS, containerDNSResolve string) error {
	src, err := os.Open(resolv)
	if err != nil {
		return nil
	}
	defer src.Close()

	dst, err := os.Create(containerDNSResolve)
	if err != nil {
		return err
	}
	defer dst.Close()

	fmt.Fprintln(dst, "# container ns resolver")
	fmt.Fprintln(dst, "nameserver", containerDNS)

	srcScan := bufio.NewScanner(src)

	for srcScan.Scan() {
		if strings.HasPrefix(srcScan.Text(), "nameserver") {
			continue
		}

		fmt.Fprintln(dst, srcScan.Text())
	}

	return dst.Close()
}
