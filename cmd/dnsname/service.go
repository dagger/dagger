package main

import (
	"os"
	"strconv"
	"strings"
)

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
