package mage

import (
	"context"
	"os"
	"os/exec"

	"github.com/magefile/mage/mg" // mg contains helpful utility functions, like Deps
)

type Dagger mg.Namespace

// Publish publishes Engine and CLI together - CLI depends on Engine
func (Dagger) Publish(ctx context.Context, version string) error {
	err := Engine{}.Publish(ctx, version)
	if err != nil {
		return err
	}

	err = Cli{}.Publish(ctx, version)

	if err != nil {
		return err
	}

	return nil
}

func call(ctx context.Context, args ...string) error {
	binary := "dagger"
	if path, ok := os.LookupEnv("_EXPERIMENTAL_DAGGER_CLI_BIN"); ok {
		binary = path
	}
	args = append([]string{"--progress=plain", "call", "--source=."}, args...)
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
