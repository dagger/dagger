package core

import (
	"context"
	"path"

	"github.com/dagger/dagger/core/pipeline"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

func (p *Project) typescriptRuntime(ctx context.Context, gw bkgw.Client, progSock *Socket, pipeline pipeline.Path) (*Container, error) {
	ctr, err := NewContainer("", pipeline, p.Platform)
	if err != nil {
		return nil, err
	}
	ctr, err = ctr.From(ctx, gw, "node:19-alpine3.17")
	if err != nil {
		return nil, err
	}

	//nolint:goconst
	workdir := "/src"
	ctr, err = ctr.UpdateImageConfig(ctx, func(cfg specs.ImageConfig) specs.ImageConfig {
		cfg.WorkingDir = absPath(cfg.WorkingDir, workdir)
		cfg.Cmd = nil
		return cfg
	})
	if err != nil {
		return nil, err
	}
	ctr, err = ctr.WithMountedDirectory(ctx, gw, workdir, p.Directory, "")
	if err != nil {
		return nil, err
	}

	ctr, err = ctr.WithExec(ctx, gw, progSock, p.Platform, ContainerExecOpts{
		Args: []string{"npm", "install", "-g", "typescript", "ts-node"},
	})
	if err != nil {
		return nil, err
	}

	ctr, err = ctr.UpdateImageConfig(ctx, func(cfg specs.ImageConfig) specs.ImageConfig {
		cfg.Entrypoint = []string{"npm", "start", "--prefix", path.Join(workdir, path.Dir(p.ConfigPath)), "--"}
		return cfg
	})
	if err != nil {
		return nil, err
	}

	ctr, err = ctr.WithExec(ctx, gw, progSock, p.Platform, ContainerExecOpts{
		Args:           []string{"npm", "install", "--prefix", path.Join(workdir, path.Dir(p.ConfigPath))},
		SkipEntrypoint: true,
	})
	if err != nil {
		return nil, err
	}

	ctr, err = ctr.WithExec(ctx, gw, progSock, p.Platform, ContainerExecOpts{
		Args:           []string{"find", path.Join(workdir, "sdk/nodejs/node_modules")},
		SkipEntrypoint: true,
	})
	if err != nil {
		return nil, err
	}

	return ctr, nil
}
