package core

// This file contains helpers for tests that need to run the Dagger CLI with
// exact arguments. It is helper-only and should not own behavior coverage.

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"testing"

	"github.com/dagger/dagger/internal/testutil"

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

func daggerNonNestedExecFail(args ...string) dagger.WithContainerFunc {
	return func(c *dagger.Container) *dagger.Container {
		return c.
			WithEnvVariable("XDG_STATE_HOME", "/tmp").
			WithMountedTemp("/tmp").
			WithExec(append([]string{"dagger"}, args...), dagger.ContainerWithExecOpts{
				ExperimentalPrivilegedNesting: false,
				Expect:                        dagger.ReturnTypeFailure,
			})
	}
}

func daggerNonNestedRun(args ...string) dagger.WithContainerFunc {
	args = append([]string{"api", "exec"}, args...)
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

// runs a dagger cli command directly on the host and captures only stdout
func hostDaggerOutput(ctx context.Context, t testing.TB, workdir string, args ...string) ([]byte, error) {
	t.Helper()
	var stderr bytes.Buffer
	cmd := hostDaggerCommand(ctx, t, workdir, args...)
	cmd.Stderr = io.MultiWriter(testutil.NewTWriter(t), &stderr)
	output, err := cmd.Output()
	if err != nil {
		err = fmt.Errorf("stdout: %s\nstderr: %s: %w", string(output), stderr.String(), err)
	}
	return output, err
}
