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

	pipCache := llb.AddMount(
		"/root/.cache/pip",
		llb.Scratch(),
		llb.AsPersistentCacheDir("pythonpipcache", llb.CacheMountShared),
	)

	return NewDirectorySt(ctx,
		llb.Image("python:3.11-alpine", llb.WithMetaResolver(gw)).
			Run(llb.Shlex(`apk add --no-cache git openssh-client`)).Root().
			Run(llb.Shlex("pip install shiv"), pipCache).Root().
			Run(
				llb.Shlex(fmt.Sprintf(
					"shiv -e dagger.server.cli:app -o /entrypoint %s --root /tmp/.shiv --reproducible",
					filepath.ToSlash(filepath.Join(workdir, filepath.Dir(p.ConfigPath), subpath)),
				)),
				llb.AddMount(workdir, contextState, llb.SourcePath(p.Directory.Dir)),
				llb.Dir(workdir),
				pipCache,
			).Root(),
		"",
		pipeline.Path{},
		platform,
		nil,
	)
}
