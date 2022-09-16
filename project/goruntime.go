package project

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"go.dagger.io/dagger/core/filesystem"
)

func (s RemoteSchema) goRuntime(ctx context.Context, subpath string) (*filesystem.Filesystem, error) {
	contextState, err := s.contextFS.ToState()
	if err != nil {
		return nil, err
	}
	workdir := "/src"
	return filesystem.FromState(ctx,
		goBase(s.gw).Run(llb.Shlex(
			fmt.Sprintf(
				`go build -o /entrypoint -ldflags '-s -d -w' %s`,
				filepath.ToSlash(filepath.Join(workdir, filepath.Dir(s.configPath), subpath)),
			)),
			llb.Dir(workdir),
			llb.AddEnv("CGO_ENABLED", "0"),
			llb.AddMount("/src", contextState),
			withGoCaching(),
			withGoPrivateRepoConfiguration(s.sshAuthSockID),
		).Root(),
		s.platform,
	)
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

func withGoPrivateRepoConfiguration(sshAuthSockID string) llb.RunOption {
	// FIXME:(sipsma) this all should be generalized to support any private go repo

	gitConfigSt := llb.Scratch().File(llb.Mkfile(".gitconfig", 0644, []byte(`
[url "ssh://git@go.dagger.io/dagger"]
  insteadOf = https://go.dagger.io/dagger
`)))

	addSSHKnownHosts, err := withGithubSSHKnownHosts()
	if err != nil {
		panic(err)
	}

	return withRunOpts(
		llb.AddMount("/root/.gitconfig", gitConfigSt, llb.SourcePath(".gitconfig"), llb.Readonly),
		llb.AddEnv("GOPRIVATE", "go.dagger.io/dagger"),
		addSSHKnownHosts,
		withSSHAuthSock(sshAuthSockID, "/ssh-agent.sock"),
	)
}
