package core

import (
	"context"
	"fmt"
	"path/filepath"

	specs "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/dagger/dagger/engine/buildkit"
)

func (mod *Module) pythonRuntime(
	ctx context.Context,
	bk *buildkit.Client,
	progSock string,
	sourceDir *Directory,
	sourceDirSubpath string,
) (*Container, error) {
	baseCtr, err := NewContainer("", mod.Pipeline, mod.Platform)
	if err != nil {
		return nil, fmt.Errorf("failed to create container: %w", err)
	}
	baseCtr, err = baseCtr.From(ctx, bk, "python:3.11-alpine")
	if err != nil {
		return nil, fmt.Errorf("failed to create container from: %w", err)
	}

    buildEnvCtr, err := baseCtr.WithExec(ctx, bk, progSock, mod.Platform, ContainerExecOpts{
		Args: []string{"apk", "add", "git"},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to install system dependencies: %w", err)
	}
	buildEnvCtr, err = buildEnvCtr.WithMountedDirectory(ctx, bk, ModSourceDirPath, sourceDir, "", false)
	if err != nil {
		return nil, fmt.Errorf("failed to mount mod source directory: %w", err)
	}
	buildEnvCtr, err = buildEnvCtr.UpdateImageConfig(ctx, func(cfg specs.ImageConfig) specs.ImageConfig {
		cfg.WorkingDir = filepath.Join(ModSourceDirPath, sourceDirSubpath)
		cfg.Cmd = nil
		return cfg
	})
	if err != nil {
		return nil, fmt.Errorf("failed to update image config: %w", err)
	}
	buildEnvCtr, err = buildEnvCtr.WithMountedCache(ctx, bk, "/root/.cache/pip", NewCache("modpythonpipcache"), nil, CacheSharingModeShared, "")
	if err != nil {
		return nil, fmt.Errorf("failed to mount pipcache: %w", err)
	}
	buildEnvCtr, err = buildEnvCtr.WithExec(ctx, bk, progSock, mod.Platform, ContainerExecOpts{
		Args: []string{"pip", "install", "shiv"},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to install shiv: %w", err)
	}
	buildEnvCtr, err = buildEnvCtr.WithExec(ctx, bk, progSock, mod.Platform, ContainerExecOpts{
		Args: []string{
			"shiv",
			"-e", "dagger.ext.cli:app",
			"-o", runtimeExecutablePath,
			"--root", "/tmp/.shiv",
			".",
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to exec shiv: %w", err)
	}

	finalEnvCtr, err := buildEnvCtr.UpdateImageConfig(ctx, func(cfg specs.ImageConfig) specs.ImageConfig {
		cfg.WorkingDir = ModSourceDirPath
		cfg.Cmd = nil
		cfg.Entrypoint = []string{runtimeExecutablePath}
		return cfg
	})
	if err != nil {
		return nil, fmt.Errorf("failed to update image config: %w", err)
	}

	return finalEnvCtr, nil
}
