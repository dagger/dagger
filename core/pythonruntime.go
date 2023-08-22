package core

import (
	"context"
	"fmt"
	"path"

	specs "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/dagger/dagger/core/pipeline"
	"github.com/dagger/dagger/engine/buildkit"
)

func pythonRuntime(
	ctx context.Context,
	bk *buildkit.Client,
	progSock string,
	pipeline pipeline.Path,
	platform specs.Platform,
	rootDir *Directory,
	configPath string,
) (*Container, error) {
	ctr, err := NewContainer("", pipeline, platform)
	if err != nil {
		return nil, fmt.Errorf("failed to create container: %w", err)
	}
	ctr, err = ctr.From(ctx, bk, "python:3.11-alpine")
	if err != nil {
		return nil, fmt.Errorf("failed to create container from: %w", err)
	}

	workdir := "/src"
	ctr, err = ctr.UpdateImageConfig(ctx, func(cfg specs.ImageConfig) specs.ImageConfig {
		cfg.WorkingDir = absPath(cfg.WorkingDir, workdir)
		cfg.Cmd = nil
		return cfg
	})
	if err != nil {
		return nil, fmt.Errorf("failed to update image config: %w", err)
	}
	ctr, err = ctr.WithMountedDirectory(ctx, bk, workdir, rootDir, "")
	if err != nil {
		return nil, fmt.Errorf("failed to mount workdir directory: %w", err)
	}

	ctr, err = ctr.WithMountedCache(ctx, bk, "/root/.cache/pip", NewCache("pythonpipcache"), nil, CacheSharingModeShared, "")
	if err != nil {
		return nil, fmt.Errorf("failed to mount pipcache: %w", err)
	}

	ctr, err = ctr.WithExec(ctx, bk, progSock, platform, ContainerExecOpts{
		Args: []string{"pip", "install", "shiv"},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to install shiv: %w", err)
	}

	ctr, err = ctr.WithExec(ctx, bk, progSock, platform, ContainerExecOpts{
		Args: []string{
			"shiv", "-e", "dagger.server.cli:app", "-o", "/entrypoint",
			path.Join(workdir, path.Dir(configPath)),
			"--root", "/tmp/.shiv",
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to exec shiv: %w", err)
	}

	ctr, err = ctr.UpdateImageConfig(ctx, func(cfg specs.ImageConfig) specs.ImageConfig {
		cfg.Entrypoint = []string{"/entrypoint"}
		return cfg
	})
	if err != nil {
		return nil, fmt.Errorf("failed to update image config: %w", err)
	}

	return ctr, nil
}
