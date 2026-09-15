package main

import (
	"context"
	"fmt"

	"dagger/toplevel/internal/dagger"
)

type Toplevel struct{}

func (_ *Toplevel) AttemptInternal(ctx context.Context) error {
	return diffSecret(
		ctx,
		dag.SetSecret("MY_SECRET", "foo"),
		dag.SetSecret("MY_SECRET", "bar"),
	)
}

func (_ *Toplevel) AttemptExternal(ctx context.Context) error {
	return diffSecret(
		ctx,
		dag.Secreter().Make("foo"),
		dag.Secreter().Make("bar"),
	)
}

func diffSecret(ctx context.Context, first, second *dagger.Secret) error {
	firstOut, err := dag.Container().
		From("alpine:3.22.1").
		WithSecretVariable("VAR", first).
		WithExec([]string{"sh", "-c", "head -c 128 /dev/random | sha256sum"}).
		Stdout(ctx)
	if err != nil {
		return err
	}

	secondOut, err := dag.Container().
		From("alpine:3.22.1").
		WithSecretVariable("VAR", second).
		WithExec([]string{"sh", "-c", "head -c 128 /dev/random | sha256sum"}).
		Stdout(ctx)
	if err != nil {
		return err
	}

	if firstOut != secondOut {
		return fmt.Errorf("%q != %q", firstOut, secondOut)
	}
	return nil
}
