package core

import (
	"context"
	"path"

	"github.com/dagger/dagger/core/pipeline"
	"github.com/dagger/dagger/engine/buildkit"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

func (p *Project) pythonRuntime(ctx context.Context, bk *buildkit.Client, progSock string, pipeline pipeline.Path) (*Container, error) {
	ctr, err := NewContainer("", pipeline, p.Platform)
	if err != nil {
		return nil, err
	}
	ctr, err = ctr.From(ctx, bk, "python:3.11-alpine")
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
	ctr, err = ctr.WithMountedDirectory(ctx, bk, workdir, p.Directory, "")
	if err != nil {
		return nil, err
	}

	ctr, err = ctr.WithMountedCache(ctx, bk, "/root/.cache/pip", NewCache("pythonpipcache"), nil, CacheSharingModeShared, "")
	if err != nil {
		return nil, err
	}

	ctr, err = ctr.WithExec(ctx, bk, progSock, p.Platform, ContainerExecOpts{
		Args: []string{"pip", "install", "shiv"},
	})
	if err != nil {
		return nil, err
	}

	ctr, err = ctr.WithExec(ctx, bk, progSock, p.Platform, ContainerExecOpts{
		Args: []string{
			"shiv", "-e", "dagger.server.cli:app", "-o", "/entrypoint",
			path.Join(workdir, path.Dir(p.ConfigPath)),
			"--root", "/tmp/.shiv",
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
