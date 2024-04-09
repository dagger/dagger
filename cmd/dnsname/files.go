package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"strings"

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

// removeFromFile removes a given entry from the dnsmasq host file
func removeFromFile(livePath, hostname string) error {
	newFile := fmt.Sprintf("%s.new", livePath)

	// clean up if things goes wrong; let it do a no-op if things go right
	defer os.RemoveAll(newFile)

	newF, err := os.Create(newFile)
	if err != nil {
		return fmt.Errorf("create new path: %w", err)
	}
	defer newF.Close()

	oldF, err := os.Open(livePath)
	if err != nil {
		return fmt.Errorf("open live path: %w", err)
	}
	defer oldF.Close()

	oldScan := bufio.NewScanner(oldF)

	var found bool
	for oldScan.Scan() {
		fields := strings.Fields(oldScan.Text())

		if len(fields) > 1 && fields[1] == hostname {
			// found the hostname; filter it out
			found = true
			continue
		}

		_, err = fmt.Fprintln(newF, oldScan.Text())
		if err != nil {
			return fmt.Errorf("write to new file: %w", err)
		}
	}

	if !found {
		logrus.Debugf("a record for %s was never found in %s", hostname, livePath)
	}

	if err := oldF.Close(); err != nil {
		return fmt.Errorf("close old file: %w", err)
	}

	if err := newF.Close(); err != nil {
		return fmt.Errorf("close new file: %w", err)
	}

	if err := os.Rename(newFile, livePath); err != nil {
		return fmt.Errorf("rename new file: %w", err)
	}

	return nil
}
