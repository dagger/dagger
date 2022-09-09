package project

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/dagger/cloak/core/filesystem"
	"github.com/moby/buildkit/client/llb"
)

func (s RemoteSchema) tsRuntime(ctx context.Context, subpath string) (*filesystem.Filesystem, error) {
	contextState, err := s.contextFS.ToState()
	if err != nil {
		return nil, err
	}

	ctrSrcPath := filepath.ToSlash(filepath.Join("/src", filepath.Dir(s.configPath), subpath))

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
		withSSHAuthSock(s.sshAuthSockID, "/ssh-agent.sock"),
		addSSHKnownHosts,
	)

	return filesystem.FromState(ctx,
		llb.Merge([]llb.State{
			llb.Image("node:16-alpine", llb.WithMetaResolver(s.gw)).
				Run(llb.Shlex(`apk add --no-cache file git openssh-client`)).Root(),
			llb.Scratch().
				File(llb.Copy(contextState, "/", "/src")),
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
		s.platform,
	)
}
