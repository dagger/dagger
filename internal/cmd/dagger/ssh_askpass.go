package daggercmd

import (
	"os"

	gitattachable "github.com/dagger/dagger/engine/session/git"
)

func runSSHAskpass() {
	socket := os.Getenv(gitattachable.SSHAskpassSocketEnv)
	if socket == "" {
		return
	}
	// This subprocess only carries a passphrase over private local IPC. Do not
	// initialize the CLI, engine connection, or telemetry exporters.
	if gitattachable.RunSSHAskpass(socket, os.Stdout) != nil {
		os.Exit(1)
	}
	os.Exit(0)
}
