package util

import (
	"strings"

	"github.com/dagger/dagger/ci/internal/dagger"
)

func ShellCmd(cmd string) dagger.WithContainerFunc {
	return func(ctr *dagger.Container) *dagger.Container {
		return ctr.WithExec([]string{"sh", "-c", cmd})
	}
}

func ShellCmds(cmds ...string) dagger.WithContainerFunc {
	return ShellCmd(strings.Join(cmds, " && "))
}
