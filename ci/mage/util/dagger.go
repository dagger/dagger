package util

import (
	"context"
	"os"
	"os/exec"
)

func DaggerCall(ctx context.Context, args ...string) error {
	binary := "dagger"
	if path, ok := os.LookupEnv("_EXPERIMENTAL_DAGGER_CLI_BIN"); ok {
		binary = path
	}
	args = append([]string{"call", "--source=."}, args...)
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
