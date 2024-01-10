package main

import (
	"context"
	"os"
	"os/exec"
)

type Caller struct{}

func (m *Caller) Test(
	ctx context.Context,
	// +optional
	run string,
) error {
	args := []string{"test", "-v", "-count=1", "."}
	if run != "" {
		args = append(args, "-run", run)
	}
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
