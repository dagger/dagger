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

func (p *Project) goRuntime(ctx context.Context, subpath string, gw bkgw.Client, platform specs.Platform) (*Directory, error) {
	contextState, err := p.Directory.State()
	if err != nil {
		return nil, err
	}

	workdir := "/src"
	return NewDirectorySt(ctx,
		goBase(gw).Run(llb.Shlex(
			fmt.Sprintf(
				`go build -o /entrypoint -ldflags '-s -d -w' %s`,
				filepath.ToSlash(filepath.Join(workdir, filepath.Dir(p.ConfigPath), subpath)),
			)),
			llb.AddEnv("CGO_ENABLED", "0"),
			llb.AddMount(workdir, contextState, llb.SourcePath(p.Directory.Dir)),
			llb.Dir(workdir),
			llb.AddMount(
				"/go/pkg/mod",
				llb.Scratch(),
				llb.AsPersistentCacheDir("gomodcache", llb.CacheMountShared),
			),
			llb.AddMount(
				"/root/.cache/go-build",
				llb.Scratch(),
				llb.AsPersistentCacheDir("gobuildcache", llb.CacheMountShared),
			),
		).Root(),
		"",
		pipeline.Path{},
		platform,
		nil,
	)
}

func goBase(gw bkgw.Client) llb.State {
	return llb.Image("golang:1.20-alpine", llb.WithMetaResolver(gw)).
		Run(llb.Shlex(`apk add --no-cache file git openssh-client`)).Root()
}
