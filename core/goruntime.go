package core

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/dagger/dagger/engine/buildkit"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

func (env *Environment) goRuntime(
	ctx context.Context,
	bk *buildkit.Client,
	progSock string,
	sourceDir *Directory,
	sourceDirSubpath string,
	workdir *Directory,
) (*Container, error) {
	baseCtr, err := NewContainer("", env.Pipeline, env.Platform)
	if err != nil {
		return nil, fmt.Errorf("failed to create container: %w", err)
	}
	baseCtr, err = baseCtr.From(ctx, bk, "golang:1.20-alpine")
	if err != nil {
		return nil, fmt.Errorf("failed to create container from: %w", err)
	}

	buildEnvCtr, err := baseCtr.WithMountedDirectory(ctx, bk, envSourceDirPath, sourceDir, "", false)
	if err != nil {
		return nil, fmt.Errorf("failed to mount env source directory: %w", err)
	}
	buildEnvCtr, err = buildEnvCtr.UpdateImageConfig(ctx, func(cfg specs.ImageConfig) specs.ImageConfig {
		cfg.WorkingDir = filepath.Join(envSourceDirPath, sourceDirSubpath)
		cfg.Cmd = nil
		return cfg
	})
	if err != nil {
		return nil, fmt.Errorf("failed to update image config: %w", err)
	}
	buildEnvCtr, err = buildEnvCtr.WithMountedCache(ctx, bk, "/go/pkg/mod", NewCache("envgomodcache"), nil, CacheSharingModeShared, "")
	if err != nil {
		return nil, fmt.Errorf("failed to mount gomodcache: %w", err)
	}
	buildEnvCtr, err = buildEnvCtr.WithMountedCache(ctx, bk, "/root/.cache/go-build", NewCache("envgobuildcache"), nil, CacheSharingModeShared, "")
	if err != nil {
		return nil, fmt.Errorf("failed to mount gobuildcache: %w", err)
	}
	buildEnvCtr, err = buildEnvCtr.WithExec(ctx, bk, progSock, env.Platform, ContainerExecOpts{
		Args: []string{
			"go", "build",
			"-o", runtimeExecutablePath,
			"-ldflags", "-s -d -w",
			".",
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to exec env go build: %w", err)
	}
	runtimeBin, err := buildEnvCtr.File(ctx, bk, runtimeExecutablePath)
	if err != nil {
		return nil, fmt.Errorf("failed to get runtime binary: %w", err)
	}

	// source dir is ro, workdir is rw
	finalEnvCtr, err := baseCtr.WithMountedDirectory(ctx, bk, envSourceDirPath, sourceDir, "", true)
	if err != nil {
		return nil, fmt.Errorf("failed to mount env source directory: %w", err)
	}
	finalEnvCtr, err = finalEnvCtr.WithMountedDirectory(ctx, bk, envWorkdirPath, workdir, "", false)
	if err != nil {
		return nil, fmt.Errorf("failed to mount env workdir: %w", err)
	}
	finalEnvCtr, err = finalEnvCtr.WithMountedFile(ctx, bk, runtimeExecutablePath, runtimeBin, "", true)
	if err != nil {
		return nil, fmt.Errorf("failed to mount runtime binary: %w", err)
	}
	finalEnvCtr, err = finalEnvCtr.UpdateImageConfig(ctx, func(cfg specs.ImageConfig) specs.ImageConfig {
		cfg.WorkingDir = envWorkdirPath
		cfg.Cmd = nil
		cfg.Entrypoint = []string{runtimeExecutablePath}
		return cfg
	})
	if err != nil {
		return nil, fmt.Errorf("failed to update image config: %w", err)
	}

	return finalEnvCtr, nil
}
