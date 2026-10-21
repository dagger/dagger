// Package dnshosts edits the hosts file that the dnsname CNI plugin keeps for
// a network's dnsmasq, which resolves container hostnames.
package dnshosts

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"github.com/gofrs/flock"
)

// Files are the dnsname plugin's files for one network.
type Files struct {
	// Hosts is dnsmasq's additional hosts file: one "IP<TAB>name[<TAB>alias...]"
	// line per container address.
	Hosts string
	// Lockfile serializes every edit of Hosts.
	Lockfile string
	// Pidfile holds dnsmasq's PID, to signal it to reload Hosts.
	Pidfile string
}

// RemoveHost removes the entries naming hostname at one of ips, and makes
// dnsmasq reload the hosts file. Entries for the same hostname at other
// addresses belong to another container and are kept.
func (f Files) RemoveHost(hostname string, ips []net.IP) error {
	lock := flock.New(f.Lockfile)
	if err := lock.Lock(); err != nil {
		return err
	}
	defer lock.Unlock()
	if _, err := Remove(f.Hosts, hostname, ips); err != nil {
		return err
	}
	return Reload(f.Pidfile)
}

// Remove removes the lines of the hosts file at path that name hostname at
// one of ips, and reports whether it found any. The caller holds the lock.
func Remove(path, hostname string, ips []net.IP) (found bool, rerr error) {
	newPath := path + ".new"
	// clean up if things go wrong; a no-op once renamed
	defer os.RemoveAll(newPath)

	newF, err := os.Create(newPath)
	if err != nil {
		return false, fmt.Errorf("create new path: %w", err)
	}
	defer newF.Close()

	oldF, err := os.Open(path)
	if err != nil {
		return false, fmt.Errorf("open live path: %w", err)
	}
	defer oldF.Close()

	scanner := bufio.NewScanner(oldF)
	for scanner.Scan() {
		if matches(scanner.Text(), hostname, ips) {
			found = true
			continue
		}
		if _, err := fmt.Fprintln(newF, scanner.Text()); err != nil {
			return false, fmt.Errorf("write to new file: %w", err)
		}
	}
	if err := scanner.Err(); err != nil {
		return false, fmt.Errorf("read live path: %w", err)
	}
	if err := oldF.Close(); err != nil {
		return false, fmt.Errorf("close old file: %w", err)
	}
	if err := newF.Close(); err != nil {
		return false, fmt.Errorf("close new file: %w", err)
	}
	if err := os.Rename(newPath, path); err != nil {
		return false, fmt.Errorf("rename new file: %w", err)
	}
	return found, nil
}

func matches(line, hostname string, ips []net.IP) bool {
	fields := strings.Fields(line)
	if len(fields) < 2 || fields[1] != hostname {
		return false
	}
	ip := net.ParseIP(fields[0])
	return ip != nil && slices.ContainsFunc(ips, ip.Equal)
}

// Reload signals dnsmasq to reload its hosts file, if it is running.
func Reload(pidfile string) error {
	proc, err := process(pidfile)
	if err != nil {
		return err
	}
	defer proc.Release()
	if !isRunning(proc) {
		return nil
	}
	return proc.Signal(syscall.SIGHUP)
}

func isRunning(proc *os.Process) bool {
	return proc.Signal(syscall.Signal(0)) == nil
}

func process(pidfile string) (*os.Process, error) {
	contents, err := os.ReadFile(pidfile)
	if err != nil {
		return nil, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(contents)))
	if err != nil {
		return nil, err
	}
	return os.FindProcess(pid)
}
