package project

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/pipeline"
	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

func (p *State) pythonRuntime(ctx context.Context, subpath string, gw bkgw.Client, platform specs.Platform) (*core.Directory, error) {
	contextState, err := p.workdir.State()
	if err != nil {
		return nil, err
	}
	workdir := "/src"
	ctrSrcPath := filepath.Join(workdir, filepath.Dir(p.configPath), subpath)
	entrypointScript := fmt.Sprintf(`#!/bin/sh
set -exu
# go to the workdir
cd %q
# run the extension
python3 main.py "$@"
`,
		ctrSrcPath)
	requirementsfile := filepath.Join(ctrSrcPath, "requirements.txt")
	return core.NewDirectorySt(ctx,
		llb.Merge([]llb.State{
			llb.Image("python:3.10-alpine", llb.WithMetaResolver(gw)).
				Run(llb.Shlex(`apk add --no-cache file git openssh-client socat`)).Root(),
			llb.Scratch().
				File(llb.Copy(contextState, p.workdir.Dir, "/src")),
		}).
			// FIXME(samalba): Install python dependencies not as root
			// FIXME(samalba): errors while installing requirements.txt will be ignored because of the `|| true`. Need to find a better way.
			Run(llb.Shlex(
				fmt.Sprintf(
					`sh -c 'test -f %q && python3 -m pip install --cache-dir=/root/.cache/pipcache -r %q || true'`,
					requirementsfile, requirementsfile,
				)),
				llb.Dir(workdir),
				llb.AddMount("/src", contextState, llb.SourcePath(p.workdir.Dir)),
				llb.AddMount(
					"/root/.cache/pipcache",
					llb.Scratch(),
					llb.AsPersistentCacheDir("pythonpipcache", llb.CacheMountShared),
				),
			).
			File(llb.Mkfile("/entrypoint", 0755, []byte(entrypointScript))),
		"",
		pipeline.Path{},
		platform,
		nil,
	)
}
