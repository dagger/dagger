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
	f, err := openFile(path)
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
		entry += "\n"
		if _, err = f.WriteString(entry); err != nil {
			return err
		}
		logrus.Debugf("appended %s: %s", path, entry)
	}
	return nil
}

// removeLineFromFile removes a given entry from the dnsmasq host file
func removeFromFile(path, podname string) error {
	var (
		keepers []string
		found   bool
	)
	backup := fmt.Sprintf("%s.old", path)
	if err := os.Rename(path, backup); err != nil {
		return err
	}
	f, err := os.Open(backup)
	if err != nil {
		//	if the open fails here, we need to revert things
		renameFile(backup, path)
		return err
	}
	defer func() {
		if err := f.Close(); err != nil {
			logrus.Errorf("unable to close %q: %v", backup, err)
		}
	}()

	oldFile := bufio.NewScanner(f)
	// Iterate the old file
	for oldFile.Scan() {
		fields := strings.Fields(oldFile.Text())
		// if the IP of the entry and the given IP dont match, it should
		// go into the new file
		if len(fields) > 1 && fields[1] != podname {
			keepers = append(keepers, fmt.Sprintf("%s\n", oldFile.Text()))
			continue
		}
		found = true
	}
	if !found {
		// We never found a matching record; non-fatal
		logrus.Debugf("a record for %s was never found in %s", podname, path)
	}
	if _, err := writeFile(path, keepers); err != nil {
		renameFile(backup, path)
		return err
	}
	if err := os.Remove(backup); err != nil {
		logrus.Errorf("unable to delete '%s': %q", backup, err)
	}
	return nil
}

// renameFile renames a file to backup
func renameFile(oldpath, newpath string) {
	if renameError := os.Rename(oldpath, newpath); renameError != nil {
		logrus.Errorf("unable to restore %q to %q: %v", oldpath, newpath, renameError)
	}
}

// writeFile writes a []string to the given path and returns the number
// of lines in the file
func writeFile(path string, content []string) (int, error) {
	var counter int
	f, err := openFile(path)
	if err != nil {
		return 0, err
	}
	defer func() {
		if err := f.Close(); err != nil {
			logrus.Errorf("unable to close %q: %v", path, err)
		}
	}()

	for _, line := range content {
		if _, err := f.WriteString(line); err != nil {
			return 0, err
		}
		counter++
	}
	return counter, nil
}

// openFile opens a file for reading
func openFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
}
