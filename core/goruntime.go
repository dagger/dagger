package core

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/dagger/dagger/engine/buildkit"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

func (mod *Module) goRuntime(
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
	baseCtr, err = baseCtr.From(ctx, bk, "golang:1.20-alpine")
	if err != nil {
		return nil, fmt.Errorf("failed to create container from: %w", err)
	}

	buildEnvCtr, err := baseCtr.WithMountedDirectory(ctx, bk, ModSourceDirPath, sourceDir, "", false)
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
	buildEnvCtr, err = buildEnvCtr.WithMountedCache(ctx, bk, "/go/pkg/mod", NewCache("modgomodcache"), nil, CacheSharingModeShared, "")
	if err != nil {
		return nil, fmt.Errorf("failed to mount gomodcache: %w", err)
	}
	buildEnvCtr, err = buildEnvCtr.WithMountedCache(ctx, bk, "/root/.cache/go-build", NewCache("modgobuildcache"), nil, CacheSharingModeShared, "")
	if err != nil {
		return nil, fmt.Errorf("failed to mount gobuildcache: %w", err)
	}
	buildEnvCtr, err = buildEnvCtr.WithExec(ctx, bk, progSock, mod.Platform, ContainerExecOpts{
		Args: []string{
			"go", "build",
			"-o", runtimeExecutablePath,
			"-ldflags", "-s -d -w",
			".",
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to exec mod go build: %w", err)
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
