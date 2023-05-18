package core

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/dagger/dagger/core/pipeline"
	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

func (p *Project) pythonRuntime(ctx context.Context, subpath string, gw bkgw.Client, platform specs.Platform) (*Directory, error) {
	contextState, err := p.Directory.State()
	if err != nil {
		return nil, err
	}
	workdir := "/src"
	appdir := filepath.Join(workdir, filepath.Dir(p.ConfigPath), subpath)
	entrypoint := fmt.Sprintf(`#!/bin/sh
set -exu
cd %q
exec python -m dagger.server "$@"
`,
		appdir)

	return NewDirectorySt(ctx,
		llb.Merge([]llb.State{
			llb.Image("python:3.11-alpine", llb.WithMetaResolver(gw)).
				Run(llb.Shlex(`apk add --no-cache file git openssh-client`)).Root(),
			llb.Scratch().
				File(llb.Copy(contextState, p.Directory.Dir, "/src")),
		}).
			Run(
				llb.Shlex("python -m pip install -r requirements.txt"),
				llb.Dir(appdir),
				llb.AddMount(
					"/root/.cache/pip",
					llb.Scratch(),
					llb.AsPersistentCacheDir("pythonpipcache", llb.CacheMountShared),
				),
			).
			File(llb.Mkfile("/entrypoint", 0755, []byte(entrypoint))),
		"",
		pipeline.Path{},
		platform,
		nil,
	)
}
