package main

import (
	"fmt"
	"net"
)

func ip(args []string) error {
	ip, err := containerIP()
	if err != nil {
		return err
	}

	fmt.Println(ip)
	return nil
}

const cidr = "10.0.0.0/8"

func containerIP() (net.IP, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}

	_, blk, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, err
	}

	for _, i := range ifaces {
		addrs, err := i.Addrs()
		if err != nil {
			return nil, err
		}

		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}

			if blk.Contains(ip) {
				return ip, nil
			}
		}
	}

	return nil, fmt.Errorf("could not determine container IP (must be in %s)", cidr)
}
