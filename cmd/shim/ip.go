package main

import (
	"fmt"
	"io"
	"net"
	"strings"
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

func ipExchange() (string, error) {
	ip, err := containerIP()
	if err != nil {
		return "", err
	}

	l, err := net.Listen("tcp", ip.String()+":0")
	if err != nil {
		return "", err
	}

	// print checker's IP so we can pass it to the service for collecting the
	// service IP
	fmt.Println(l.Addr())

	conn, err := l.Accept()
	if err != nil {
		return "", err
	}

	svcIPPayload, err := io.ReadAll(conn)
	if err != nil {
		return "", err
	}

	svcIP := strings.TrimSpace(string(svcIPPayload))

	// print service IP; this is read by the outer health check process and
	// stored for passing to clients
	fmt.Println(svcIP)

	return svcIP, nil
}

func reportIP(addr string) error {
	ip, err := containerIP()
	if err != nil {
		return fmt.Errorf("get container IP: %w", err)
	}

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}

	_, err = fmt.Fprintln(conn, ip.String())
	if err != nil {
		return fmt.Errorf("write: %w", err)
	}

	return conn.Close()
}
