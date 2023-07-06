package core

import (
	"context"
	"path"

	"github.com/dagger/dagger/core/pipeline"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

func (p *Project) goRuntime(ctx context.Context, gw bkgw.Client, progSock *Socket, pipeline pipeline.Path) (*Container, error) {
	ctr, err := NewContainer("", pipeline, p.Platform)
	if err != nil {
		return nil, err
	}
	ctr, err = ctr.From(ctx, gw, "golang:1.20-alpine")
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

	ctr, err = ctr.WithMountedCache(ctx, gw, "/go/pkg/mod", NewCache("gomodcache"), nil, CacheSharingModeShared, "")
	if err != nil {
		return nil, err
	}
	ctr, err = ctr.WithMountedCache(ctx, gw, "/root/.cache/go-build", NewCache("gobuildcache"), nil, CacheSharingModeShared, "")
	if err != nil {
		return nil, err
	}

	ctr, err = ctr.WithExec(ctx, gw, progSock, p.Platform, ContainerExecOpts{
		Args: []string{
			"go", "build", "-o", "/entrypoint", "-ldflags", "-s -d -w",
			path.Join(workdir, path.Dir(p.ConfigPath)),
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
