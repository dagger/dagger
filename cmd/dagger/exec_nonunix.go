//go:build !unix
// +build !unix

package main

import "os/exec"

func ensureChildProcessesAreKilled(cmd *exec.Cmd) {
}
