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

func (p *State) tsRuntime(ctx context.Context, subpath string, gw bkgw.Client, platform specs.Platform, sshAuthSockID string) (*core.Directory, error) {
	payload, err := p.workdir.ID.Decode()
	if err != nil {
		return nil, err
	}
	contextState, err := payload.State()
	if err != nil {
		return nil, err
	}

	ctrSrcPath := filepath.ToSlash(filepath.Join("/src", filepath.Dir(p.configPath), subpath))

	addSSHKnownHosts, err := withGithubSSHKnownHosts()
	if err != nil {
		return nil, err
	}
	baseRunOpts := withRunOpts(
		llb.AddEnv("YARN_CACHE_FOLDER", "/cache/yarn"),
		llb.AddMount(
			"/cache/yarn",
			llb.Scratch(),
			llb.AsPersistentCacheDir("yarn", llb.CacheMountLocked),
		),
		withSSHAuthSock(sshAuthSockID, "/ssh-agent.sock"),
		addSSHKnownHosts,
	)

	return core.NewDirectory(ctx,
		llb.Merge([]llb.State{
			llb.Image("node:16-alpine", llb.WithMetaResolver(gw)).
				Run(llb.Shlex(`apk add --no-cache file git openssh-client`)).Root(),
			llb.Scratch().
				File(llb.Copy(contextState, payload.Dir, "/src")),
		}).
			Run(llb.Shlex(fmt.Sprintf(`sh -c 'cd %s && yarn install'`, ctrSrcPath)), baseRunOpts).
			Run(llb.Shlex(fmt.Sprintf(`sh -c 'cd %s && yarn build'`, ctrSrcPath)), baseRunOpts).
			File(llb.Mkfile(
				"/entrypoint",
				0755,
				[]byte(fmt.Sprintf(
					"#!/bin/sh\nset -e; cd %s && node --unhandled-rejections=strict dist/index.js",
					ctrSrcPath,
				)),
			)),
		"",
		platform,
	)
}
