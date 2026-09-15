//go:build !unix
// +build !unix

package daggercmd

import (
	"os"
	"os/exec"
)

func ensureChildProcessesAreKilled(cmd *exec.Cmd) {
}

func execCLI(binPath string, args, env []string) error {
	cmd := exec.Command(binPath, args[1:]...)
	cmd.Args = args
	cmd.Env = env
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if exitErr, ok := err.(*exec.ExitError); ok {
		os.Exit(exitErr.ExitCode())
	}
	return err
}
