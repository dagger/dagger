package main

import (
	"context"
	"dagger/go-checker/internal/dagger"
	"errors"
)

type GoChecker struct{}

func (checker GoChecker) Check(ctx context.Context, dir *dagger.Directory) error {
	execOpts := dagger.ContainerWithExecOpts{Expect: dagger.ReturnTypeAny}
	check := dag.Go(dir).
		Env().
		WithExec([]string{"sh", "-c", "go mod tidy && go build ./..."}, execOpts)
	code, err := check.ExitCode(ctx)
	if err != nil {
		return err
	}
	if code == 0 {
		return nil
	}
	stderr, err := check.Stderr(ctx)
	if err != nil {
		return err
	}
	return errors.New(stderr)
}
