package core

// Workspace alignment: aligned; helper-only file for legacy module command shims kept during cleanup.
// Scope: Historical top-level module command wrappers and host helpers that still rewrite old mutation verbs.
// Intent: Keep legacy shim behavior visible and quarantined while exact-by-intent helpers become the default for new tests.

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"dagger.io/dagger"
)

// daggerExec preserves historical top-level module mutation syntax for older
// integration coverage. New workspace-era tests should prefer daggerExecRaw or
// the explicit module/workspace helpers.
func daggerExec(args ...string) dagger.WithContainerFunc {
	args = rewriteLegacyModuleCommandTestArgs(args)
	return daggerExecRaw(args...)
}

func daggerExecFail(args ...string) dagger.WithContainerFunc {
	args = rewriteLegacyModuleCommandTestArgs(args)
	return func(c *dagger.Container) *dagger.Container {
		return c.WithExec(append([]string{"dagger"}, args...), dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
			Expect:                        dagger.ReturnTypeFailure,
		})
	}
}

func daggerNonNestedExec(args ...string) dagger.WithContainerFunc {
	args = rewriteLegacyModuleCommandTestArgs(args)
	return func(c *dagger.Container) *dagger.Container {
		return c.
			WithEnvVariable("XDG_STATE_HOME", "/tmp").
			WithMountedTemp("/tmp").
			WithExec(append([]string{"dagger"}, args...), dagger.ContainerWithExecOpts{
				ExperimentalPrivilegedNesting: false,
			})
	}
}

func daggerNonNestedRun(args ...string) dagger.WithContainerFunc {
	args = append([]string{"run"}, args...)
	return daggerNonNestedExec(args...)
}

// hostDaggerCommand preserves the same legacy command rewriting as daggerExec.
// New workspace-era tests should prefer hostDaggerCommandRaw or explicit
// module/workspace helpers.
func hostDaggerCommand(ctx context.Context, t testing.TB, workdir string, args ...string) *exec.Cmd {
	args = rewriteLegacyModuleCommandTestArgs(args)
	return hostDaggerCommandRaw(ctx, t, workdir, args...)
}

// runs a dagger cli command directly on the host, rather than in an exec
func hostDaggerExec(ctx context.Context, t testing.TB, workdir string, args ...string) ([]byte, error) {
	t.Helper()
	cmd := hostDaggerCommand(ctx, t, workdir, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		err = fmt.Errorf("%s: %w", string(output), err)
	}
	return output, err
}

// Most integration tests still express module mutation through the old top-level
// commands. Translate those calls to `dagger module ...` while the product
// command surface moves to workspace-first top-level commands.
func rewriteLegacyModuleCommandTestArgs(args []string) []string {
	cmdIdx := firstTestCommandArg(args)
	if cmdIdx < 0 {
		return args
	}

	subcmd := ""
	switch args[cmdIdx] {
	case "init":
		subcmd = "init"
	case "install", "use":
		subcmd = "install"
	case "update":
		subcmd = "update"
	default:
		return args
	}

	rewritten := make([]string, 0, len(args)+1)
	rewritten = append(rewritten, args[:cmdIdx]...)
	rewritten = append(rewritten, "module", subcmd)
	rewritten = append(rewritten, args[cmdIdx+1:]...)
	return rewritten
}

func firstTestCommandArg(args []string) int {
	skipValue := false
	for i, arg := range args {
		if skipValue {
			skipValue = false
			continue
		}
		if arg == "--" {
			return -1
		}
		if strings.HasPrefix(arg, "-") {
			switch arg {
			case "--workdir", "--progress", "--lock", "--dot-output", "--dot-focus-field", "--interactive-command":
				skipValue = true
			}
			continue
		}
		return i
	}
	return -1
}
