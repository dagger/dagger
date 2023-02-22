package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/pkg/errors"
	"golang.org/x/sys/unix"
)

// newDNSMasqFile creates a new instance of a dnsNameFile
func newDNSMasqFile(domainName, networkInterface, networkName string) (dnsNameFile, error) {
	dnsMasqBinary, err := exec.LookPath("dnsmasq")
	if err != nil {
		return dnsNameFile{}, errors.Errorf("the dnsmasq cni plugin requires the dnsmasq binary be in PATH")
	}
	masqConf := dnsNameFile{
		ConfigFile:       makePath(networkName, confFileName),
		Domain:           domainName,
		PidFile:          makePath(networkName, pidFileName),
		NetworkInterface: networkInterface,
		AddOnHostsFile:   makePath(networkName, hostsFileName),
		Binary:           dnsMasqBinary,
	}
	return masqConf, nil
}

// hup sends a sighup to a running dnsmasq to reload its hosts file. if
// there is no instance of the dnsmasq, then it simply starts it.
func (d dnsNameFile) hup() error {
	// First check for pidfile; if it does not exist, we just
	// start the service
	if _, err := os.Stat(d.PidFile); os.IsNotExist(err) {
		return d.start()
	}
	pid, err := d.getProcess()
	if err != nil {
		return err
	}
	if !isRunning(pid) {
		return d.start()
	}
	return pid.Signal(unix.SIGHUP)
}

// isRunning sends a signal 0 to the pid to determine if it
// responds or not
func isRunning(pid *os.Process) bool {
	if err := pid.Signal(syscall.Signal(0)); err != nil {
		return false
	}
	return true
}

// start starts the dnsmasq instance.
func (d dnsNameFile) start() error {
	args := []string{
		"-u",
		"root",
		fmt.Sprintf("--conf-file=%s", d.ConfigFile),
	}
	cmd := exec.Command(d.Binary, args...) //nolint:gosec
	if b, err := cmd.CombinedOutput(); err != nil {
		return errors.Wrapf(err, "dnsname error: dnsmasq failed with %q", b)
	}
	return nil
}

// getProcess reads the PID for the dnsmasq instance and returns an
// *os.Process. Returns an error if the PID does not exist.
func (d dnsNameFile) getProcess() (*os.Process, error) {
	pidFileContents, err := os.ReadFile(d.PidFile)
	if err != nil {
		return nil, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(pidFileContents)))
	if err != nil {
		return nil, err
	}
	return os.FindProcess(pid)
}

// makePath formats a path name given a domain and suffix
func makePath(networkName, fileName string) string {
	// the generic path for where conf, host, pid files are kept is:
	// /run/containers/cni/dnsmasq/<network-name>/
	return filepath.Join(dnsNameConfPath(), networkName, fileName)
}
