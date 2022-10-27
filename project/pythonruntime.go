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

func (p *State) pythonRuntime(ctx context.Context, subpath string, gw bkgw.Client, platform specs.Platform, sshAuthSockID string) (*core.Directory, error) {
	// TODO(vito): handle platform?
	payload, err := p.workdir.ID.Decode()
	if err != nil {
		return nil, err
	}
	contextState, err := payload.State()
	if err != nil {
		return nil, err
	}
	workdir := "/src"
	addSSHKnownHosts, err := withGithubSSHKnownHosts()
	if err != nil {
		return nil, err
	}
	ctrSrcPath := filepath.Join(workdir, filepath.Dir(p.configPath), subpath)
	entrypointScript := fmt.Sprintf(`#!/bin/sh
set -exu
# go to the workdir
cd %q
# redirect local unix socket (graphql server) to a bound tcp port
socat TCP-LISTEN:8080 UNIX-CONNECT:/dagger.sock &
# run the extension
exec dagger-py "$@"
`,
		ctrSrcPath)
	requirementsfile := filepath.Join(ctrSrcPath, "requirements.txt")
	return core.NewDirectory(ctx,
		llb.Merge([]llb.State{
			llb.Image("python:3.10.6-alpine", llb.WithMetaResolver(gw)).
				Run(llb.Shlex(`apk add --no-cache file git openssh-client socat`)).Root(),
			llb.Scratch().
				File(llb.Copy(contextState, payload.Dir, "/src")),
		}).
			// FIXME(samalba): Install python dependencies not as root
			// FIXME(samalba): errors while installing requirements.txt will be ignored because of the `|| true`. Need to find a better way.
			Run(llb.Shlex(
				fmt.Sprintf(
					`sh -c 'test -f %q && python3 -m pip install --cache-dir=/root/.cache/pipcache -r %q || true'`,
					requirementsfile, requirementsfile,
				)),
				llb.Dir(workdir),
				llb.AddMount("/src", contextState, llb.SourcePath(payload.Dir)),
				llb.AddMount(
					"/root/.cache/pipcache",
					llb.Scratch(),
					llb.AsPersistentCacheDir("pythonpipcache", llb.CacheMountShared),
				),
				withSSHAuthSock(sshAuthSockID, "/ssh-agent.sock"),
				addSSHKnownHosts,
			).
			File(llb.Mkfile("/entrypoint", 0755, []byte(entrypointScript))),
		"",
		platform,
	)
}
