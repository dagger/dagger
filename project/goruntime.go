package project

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/dagger/dagger/core"
	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

func (p *State) goRuntime(ctx context.Context, subpath string, session *core.Session, platform specs.Platform) (*core.Directory, error) {
	// TODO(vito): handle platform?
	payload, err := p.workdir.ID.Decode()
	if err != nil {
		return nil, err
	}

	contextState, err := payload.State()
	if err != nil {
		return nil, err
	}

	var dir *core.Directory
	_, err = session.WithLocalDirs(payload.LocalDirs).Build(ctx, func(ctx context.Context, gw bkgw.Client) (*bkgw.Result, error) {
		workdir := "/src"
		dir, err = core.NewDirectory(ctx,
			goBase(gw).Run(llb.Shlex(
				fmt.Sprintf(
					`go build -o /entrypoint -ldflags '-s -d -w' %s`,
					filepath.ToSlash(filepath.Join(workdir, filepath.Dir(p.configPath), subpath)),
				)),
				llb.Dir(workdir),
				llb.AddEnv("CGO_ENABLED", "0"),
				llb.AddMount("/src", contextState, llb.SourcePath(payload.Dir)),
				withGoCaching(),
			).Root(),
			"",
			platform,
			payload.LocalDirs,
		)
		if err != nil {
			return nil, err
		}

		return bkgw.NewResult(), nil
	})
	if err != nil {
		return nil, err
	}

	return dir, nil
}

func goBase(gw bkgw.Client) llb.State {
	return llb.Image("golang:1.19.1-alpine", llb.WithMetaResolver(gw)).
		Run(llb.Shlex(`apk add --no-cache file git openssh-client`)).Root()
}

func withGoCaching() llb.RunOption {
	return withRunOpts(
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
	)
}
