package core

// Workspace alignment: aligned; helper-only file for direct Dagger CLI execution.
// Scope: Container and host helpers that run the Dagger CLI with the exact arguments provided by the test.
// Intent: Keep command intent visible at each callsite.

import (
	"context"
	"fmt"
	"os/exec"
	"testing"

	"dagger.io/dagger"
)

func daggerExec(args ...string) dagger.WithContainerFunc {
	return daggerExecRaw(args...)
}

func daggerExecFail(args ...string) dagger.WithContainerFunc {
	return func(c *dagger.Container) *dagger.Container {
		return c.WithExec(append([]string{"dagger"}, args...), dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
			Expect:                        dagger.ReturnTypeFailure,
		})
	}
}

func daggerNonNestedExec(args ...string) dagger.WithContainerFunc {
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

func hostDaggerCommand(ctx context.Context, t testing.TB, workdir string, args ...string) *exec.Cmd {
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
