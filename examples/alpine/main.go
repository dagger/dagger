package main

import (
	"context"
	"fmt"

	"github.com/dagger/cloak/examples/alpine/gen/core"
	"github.com/dagger/cloak/sdk/go/dagger"
)

func (r *alpine) build(ctx context.Context, pkgs []string) (*dagger.Filesystem, error) {
	// start with Alpine base
	output, err := core.Image(ctx, "alpine:3.15")
	if err != nil {
		return nil, err
	}

	fs := &output.Core.Image

	// install each of the requested packages
	for _, pkg := range pkgs {
		output, err := core.Exec(ctx, fs.ID, core.ExecInput{
			Args:    []string{"apk", "add", "-U", "--no-cache", pkg},
			Workdir: "/mnt",
		})
		if err != nil {
			return nil, fmt.Errorf("failed to install %s: %s", pkg, err)
		}
		fs = output.Core.Filesystem.Exec.Fs
	}

	return fs, nil
}
