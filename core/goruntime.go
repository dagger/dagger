package core

import (
	"context"
	"path"

	"github.com/dagger/dagger/core/pipeline"
	"github.com/dagger/dagger/engine/buildkit"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

func goRuntime(
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
		return nil, err
	}
	ctr, err = ctr.From(ctx, bk, "golang:1.20-alpine")
	if err != nil {
		return nil, err
	}

	workdir := "/src"
	ctr, err = ctr.UpdateImageConfig(ctx, func(cfg specs.ImageConfig) specs.ImageConfig {
		cfg.WorkingDir = absPath(cfg.WorkingDir, workdir)
		cfg.Cmd = nil
		return cfg
	})
	if err != nil {
		return nil, err
	}
	ctr, err = ctr.WithMountedDirectory(ctx, bk, workdir, rootDir, "")
	if err != nil {
		return nil, err
	}

	ctr, err = ctr.WithMountedCache(ctx, bk, "/go/pkg/mod", NewCache("gomodcache"), nil, CacheSharingModeShared, "")
	if err != nil {
		return nil, err
	}
	ctr, err = ctr.WithMountedCache(ctx, bk, "/root/.cache/go-build", NewCache("gobuildcache"), nil, CacheSharingModeShared, "")
	if err != nil {
		return nil, err
	}

	ctr, err = ctr.WithExec(ctx, bk, progSock, platform, ContainerExecOpts{
		Args: []string{
			"go", "build", "-o", "/entrypoint", "-ldflags", "-s -d -w",
			path.Join(workdir, path.Dir(configPath)),
		},
	})
	if err != nil {
		return nil, err
	}

	ctr, err = ctr.UpdateImageConfig(ctx, func(cfg specs.ImageConfig) specs.ImageConfig {
		cfg.Entrypoint = []string{"/entrypoint"}
		return cfg
	})
	if err != nil {
		return nil, err
	}

	return ctr, nil
}
