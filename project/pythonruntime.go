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
	return filesystem.FromState(ctx,
		llb.Image("python:3.10.6-alpine", llb.WithMetaResolver(s.gw)).
			Run(llb.Shlex(
				fmt.Sprintf(
					`pip install --cache-dir=/root/.cache/pipcache -r %s`,
					filepath.Join(workdir, filepath.Dir(s.configPath), subpath, "requirements.txt"),
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
			).Root(),
		s.platform,
	)
}
