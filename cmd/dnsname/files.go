package main

import (
	"fmt"
	"net"
	"os"

	"github.com/sirupsen/logrus"
)

// appendToFile appends a new entry to the dnsmasqs hosts file
func appendToFile(path, podname string, aliases []string, ips []*net.IPNet) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer func() {
		if err := f.Close(); err != nil {
			logrus.Errorf("failed to close file %q: %v", path, err)
		}
	}()
	for _, ip := range ips {
		entry := fmt.Sprintf("%s\t%s", ip.IP.String(), podname)
		for _, alias := range aliases {
			entry += fmt.Sprintf("\t%s", alias)
		}
		if _, err = fmt.Fprintln(f, entry); err != nil {
			return err
		}
		logrus.Debugf("appended %s: %s", path, entry)
	}
	return nil
}
