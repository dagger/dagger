package core

import (
	"context"
	"fmt"
	"github.com/dagger/dagger/core/pipeline"
	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"path/filepath"
)

func (p *Project) typescriptRuntime(ctx context.Context, subpath string, gw bkgw.Client, platform specs.Platform) (*Directory, error) {
	contextState, err := p.Directory.State()
	if err != nil {
		return nil, err
	}

	workdir := "/src"
	appdir := filepath.Join(workdir, filepath.Dir(p.ConfigPath), subpath)
	entrypoint := fmt.Sprintf(`#!/bin/sh
set -exu
cd %q
exec npm start -- "$@"
`, appdir)

	return NewDirectorySt(ctx,
		llb.Merge([]llb.State{
			llb.Image("node:19-alpine3.17", llb.WithMetaResolver(gw)),
			llb.Scratch().File(llb.Copy(contextState, p.Directory.Dir, "/src")),
		}).
			Run(
				llb.Shlex("npm install -g typescript ts-node"),
			).
			Run(
				llb.Shlex("npm install"),
				llb.Dir(appdir),
			).
			File(llb.Mkfile("/entrypoint", 0755, []byte(entrypoint))),
		"",
		pipeline.Path{},
		platform,
		nil)
}
