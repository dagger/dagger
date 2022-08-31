package project

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/containerd/containerd/platforms"
	"github.com/dagger/cloak/core/filesystem"
	"github.com/moby/buildkit/client/llb"
	dockerfilebuilder "github.com/moby/buildkit/frontend/dockerfile/builder"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
	"github.com/moby/buildkit/util/sshutil"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

// TODO:(sipsma) SDKs should be pluggable extensions, not hardcoded LLB here. The implementation here is a temporary bridge from the previous hardcoded Dockerfiles to the sdk-as-extension model.

func goRuntime(ctx context.Context, contextFS *filesystem.Filesystem, cfgPath, sourcePath string, p specs.Platform, gw bkgw.Client, sshAuthSockID string) (*filesystem.Filesystem, error) {
	contextState, err := contextFS.ToState()
	if err != nil {
		return nil, err
	}
	workdir := "/src"
	addSSHKnownHosts, err := withGithubSSHKnownHosts()
	if err != nil {
		return nil, err
	}
	return filesystem.FromState(ctx,
		llb.Image("golang:1.18.2-alpine", llb.WithMetaResolver(gw)).
			Run(llb.Shlex(`apk add --no-cache file git openssh-client`)).Root().
			// FIXME:(sipsma) should be generalized to support any private go repo
			File(llb.Mkfile("/root/.gitconfig", 0644, []byte(`
[url "ssh://git@github.com/dagger/cloak"]
  insteadOf = https://github.com/dagger/cloak
`))).
			Run(llb.Shlex(
				fmt.Sprintf(
					`go build -o /entrypoint -ldflags '-s -d -w' %s`,
					filepath.Join(workdir, filepath.Dir(cfgPath), sourcePath),
				)),
				llb.Dir(workdir),
				llb.AddEnv("GOMODCACHE", "/root/.cache/gocache"),
				llb.AddEnv("CGO_ENABLED", "0"),
				llb.AddMount("/src", contextState),
				llb.AddMount(
					"/root/.cache/gocache",
					llb.Scratch(),
					llb.AsPersistentCacheDir("gomodcache", llb.CacheMountShared),
				),
				// FIXME:(sipsma) should be generalized to support any private go repo
				llb.AddEnv("GOPRIVATE", "github.com/dagger/cloak"),
				llb.AddSSHSocket(
					llb.SSHID(sshAuthSockID),
					llb.SSHSocketTarget("/ssh-agent.sock"),
				),
				llb.AddEnv("SSH_AUTH_SOCK", "/ssh-agent.sock"),
				addSSHKnownHosts,
			).Root(),
		p,
	)
}

func tsRuntime(ctx context.Context, contextFS *filesystem.Filesystem, cfgPath, sourcePath string, p specs.Platform, gw bkgw.Client, sshAuthSockID string) (*filesystem.Filesystem, error) {
	contextState, err := contextFS.ToState()
	if err != nil {
		return nil, err
	}

	ctrSrcPath := filepath.Join("/src", filepath.Dir(cfgPath), sourcePath)

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
		llb.AddSSHSocket(
			llb.SSHID(sshAuthSockID),
			llb.SSHSocketTarget("/ssh-agent.sock"),
		),
		llb.AddEnv("SSH_AUTH_SOCK", "/ssh-agent.sock"),
		addSSHKnownHosts,
	)

	return filesystem.FromState(ctx,
		llb.Merge([]llb.State{
			llb.Image("node:16-alpine", llb.WithMetaResolver(gw)).
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
		p,
	)
}

func dockerfileRuntime(ctx context.Context, contextFS *filesystem.Filesystem, cfgPath, sourcePath string, p specs.Platform, gw bkgw.Client) (*filesystem.Filesystem, error) {
	def, err := contextFS.ToDefinition()
	if err != nil {
		return nil, err
	}

	opts := map[string]string{
		"platform": platforms.Format(p),
		"filename": filepath.Join(filepath.Dir(cfgPath), sourcePath, "Dockerfile"),
	}
	inputs := map[string]*pb.Definition{
		dockerfilebuilder.DefaultLocalNameContext:    def,
		dockerfilebuilder.DefaultLocalNameDockerfile: def,
	}
	res, err := gw.Solve(ctx, bkgw.SolveRequest{
		Frontend:       "dockerfile.v0",
		FrontendOpt:    opts,
		FrontendInputs: inputs,
	})
	if err != nil {
		return nil, err
	}

	bkref, err := res.SingleRef()
	if err != nil {
		return nil, err
	}
	st, err := bkref.ToState()
	if err != nil {
		return nil, err
	}

	return filesystem.FromState(ctx, st, p)
}

type runOptionFunc func(*llb.ExecInfo)

func (fn runOptionFunc) SetRunOption(ei *llb.ExecInfo) {
	fn(ei)
}

func withRunOpts(runOpts ...llb.RunOption) llb.RunOption {
	return runOptionFunc(func(ei *llb.ExecInfo) {
		for _, runOpt := range runOpts {
			runOpt.SetRunOption(ei)
		}
	})
}

func withGithubSSHKnownHosts() (llb.RunOption, error) {
	knownHosts, err := sshutil.SSHKeyScan("github.com")
	if err != nil {
		return nil, err
	}

	return withRunOpts(
		llb.AddMount("/tmp/known_hosts",
			llb.Scratch().File(llb.Mkfile("known_hosts", 0600, []byte(knownHosts))),
			llb.SourcePath("/known_hosts"),
			llb.ForceNoOutput,
		),
		llb.AddEnv("GIT_SSH_COMMAND", "ssh -o UserKnownHostsFile=/tmp/known_hosts"),
	), nil
}
