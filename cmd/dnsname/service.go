package main

import (
	"os"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// hup sends a sighup to a running dnsmasq to reload its hosts file. if
// there is no instance of the dnsmasq, then it simply starts it.
func hup(pidfile string) error {
	pid, err := getProcess(pidfile)
	if err != nil {
		return err
	}
	if !isRunning(pid) {
		return nil
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

// getProcess reads the PID for the dnsmasq instance and returns an
// *os.Process. Returns an error if the PID does not exist.
func getProcess(pidfile string) (*os.Process, error) {
	pidFileContents, err := os.ReadFile(pidfile)
	if err != nil {
		return nil, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(pidFileContents)))
	if err != nil {
		return nil, err
	}
	return os.FindProcess(pid)
}
