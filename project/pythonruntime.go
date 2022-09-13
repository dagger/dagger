package project

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/dagger/cloak/core/filesystem"
	"github.com/moby/buildkit/client/llb"
)

func (s RemoteSchema) pythonRuntime(ctx context.Context, subpath string) (*filesystem.Filesystem, error) {
	contextState, err := s.contextFS.ToState()
	if err != nil {
		return nil, err
	}
	workdir := "/src"
	addSSHKnownHosts, err := withGithubSSHKnownHosts()
	if err != nil {
		return nil, err
	}
	ctrSrcPath := filepath.Join(workdir, filepath.Dir(s.configPath), subpath)
	requirementsfile := filepath.Join(ctrSrcPath, "requirements.txt")
	return filesystem.FromState(ctx,
		llb.Merge([]llb.State{
			llb.Image("python:3.10.6-alpine", llb.WithMetaResolver(s.gw)).
				Run(llb.Shlex(`apk add --no-cache file git openssh-client`)).Root(),
			llb.Scratch().
				File(llb.Copy(contextState, "/", "/src")),
		}).
			// FIXME(samalba): Install python dependencies not as root
			Run(llb.Shlex(
				fmt.Sprintf(
					`sh -c 'test -f %q && python3 -m pip install --cache-dir=/root/.cache/pipcache -r %q || true'`,
					requirementsfile, requirementsfile,
				)),
				llb.Dir(workdir),
				llb.AddMount("/src", contextState),
				llb.AddMount(
					"/root/.cache/pipcache",
					llb.Scratch(),
					llb.AsPersistentCacheDir("pythonpipcache", llb.CacheMountShared),
				),
				withSSHAuthSock(s.sshAuthSockID, "/ssh-agent.sock"),
				addSSHKnownHosts,
			).
			File(llb.Mkfile(
				"/entrypoint",
				0755,
				[]byte(fmt.Sprintf(
					"#!/bin/sh\nset -e; cd %q && python3 main.py",
					ctrSrcPath,
				)),
			)),
		s.platform,
	)
}
