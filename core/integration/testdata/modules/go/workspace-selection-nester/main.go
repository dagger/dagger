package main

import (
	"context"
	"strings"

	"dagger/nester/internal/dagger"
)

func New(greeting string) *Nester {
	return &Nester{Message: greeting}
}

type Nester struct {
	Message string
}

func (m *Nester) Greeting() string {
	return m.Message
}

func (m *Nester) NestedWorkspace(ctx context.Context, cli *dagger.File) (string, error) {
	return m.nested(ctx, cli, []string{"query"}, dagger.ContainerWithExecOpts{
		ExperimentalPrivilegedNesting: true,
		Stdin:                         "{currentWorkspace{cwd configFile}}",
	})
}

func (m *Nester) NestedGreeting(ctx context.Context, cli *dagger.File) (string, error) {
	return m.nested(ctx, cli, []string{"call", "greeting"}, dagger.ContainerWithExecOpts{
		ExperimentalPrivilegedNesting: true,
	})
}

func (m *Nester) nested(ctx context.Context, cli *dagger.File, args []string, opts dagger.ContainerWithExecOpts) (string, error) {
	execArgs := append([]string{"dagger", "--progress=report"}, args...)
	out, err := dag.Container().
		From("alpine:3.22.1").
		WithMountedFile("/bin/dagger", cli).
		WithExec([]string{"mkdir", "-p", "/empty"}).
		WithWorkdir("/empty").
		WithExec(execArgs, opts).
		Stdout(ctx)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}
