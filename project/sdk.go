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
	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

// TODO:(sipsma) SDKs should be pluggable extensions, not hardcoded LLB here. The implementation here is a temporary bridge from the previous hardcoded Dockerfiles to the sdk-as-extension model.

func goRuntime(ctx context.Context, contextFS *filesystem.Filesystem, cfgPath, sourcePath string, p specs.Platform, gw bkgw.Client) (*filesystem.Filesystem, error) {
	contextState, err := contextFS.ToState()
	if err != nil {
		return nil, err
	}
	workdir := "/src"
	return filesystem.FromState(ctx,
		llb.Image("golang:1.18.2-alpine", llb.WithMetaResolver(gw)).
			Run(llb.Shlex(`apk add --no-cache file git`)).Root().
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
			).Root(),
		p,
	)
}

func tsRuntime(ctx context.Context, contextFS *filesystem.Filesystem, cfgPath, sourcePath string, p specs.Platform, gw bkgw.Client) (*filesystem.Filesystem, error) {
	contextState, err := contextFS.ToState()
	if err != nil {
		return nil, err
	}
	base := llb.Image("node:16-alpine", llb.WithMetaResolver(gw))
	build := base.
		Run(llb.Shlex(`apk add --no-cache file git`)).Root().
		File(llb.Mkdir("/app/src", 0755, llb.WithParents(true))).
		File(llb.Mkdir("/sdk", 0755, llb.WithParents(true))).
		File(llb.Copy(contextState, filepath.Join(filepath.Dir(cfgPath), sourcePath, "package.json"), "/app/src/package.json")).
		File(llb.Copy(contextState, filepath.Join(filepath.Dir(cfgPath), sourcePath, "yarn.lock"), "/app/src/yarn.lock")).
		File(llb.Copy(contextState, "sdk/nodejs", "/sdk/nodejs")).
		Run(llb.Shlex(`yarn --cwd /sdk/nodejs/dagger`),
			llb.Dir("/app/src"),
			llb.AddEnv("YARN_CACHE_FOLDER", "/cache/yarn"),
			llb.AddMount(
				"/cache/yarn",
				llb.Scratch(),
				llb.AsPersistentCacheDir("yarn", llb.CacheMountLocked),
			),
		).Root().
		Run(llb.Shlex(`yarn --cwd /sdk/nodejs/dagger build`),
			llb.Dir("/app/src"),
			llb.AddEnv("YARN_CACHE_FOLDER", "/cache/yarn"),
			llb.AddMount(
				"/cache/yarn",
				llb.Scratch(),
				llb.AsPersistentCacheDir("yarn", llb.CacheMountLocked),
			),
		).Root().
		Run(llb.Shlex(`yarn`),
			llb.Dir("/app/src"),
			llb.AddEnv("YARN_CACHE_FOLDER", "/cache/yarn"),
			llb.AddMount(
				"/cache/yarn",
				llb.Scratch(),
				llb.AsPersistentCacheDir("yarn", llb.CacheMountLocked),
			),
		).Root().
		File(llb.Copy(contextState, filepath.Join(filepath.Dir(cfgPath), sourcePath), "/app/src/", &llb.CopyInfo{CopyDirContentsOnly: true})).
		Run(llb.Shlex(`yarn upgrade dagger`),
			llb.Dir("/app/src"),
			llb.AddEnv("YARN_CACHE_FOLDER", "/cache/yarn"),
			llb.AddMount(
				"/cache/yarn",
				llb.Scratch(),
				llb.AsPersistentCacheDir("yarn", llb.CacheMountLocked),
			),
		).Root().
		Run(llb.Shlex(`yarn build`),
			llb.Dir("/app/src"),
			llb.AddEnv("YARN_CACHE_FOLDER", "/cache/yarn"),
			llb.AddMount(
				"/cache/yarn",
				llb.Scratch(),
				llb.AsPersistentCacheDir("yarn", llb.CacheMountLocked),
			),
		).Root()
	return filesystem.FromState(ctx,
		base.
			File(llb.Mkdir("/app/src", 0755, llb.WithParents(true))).
			File(llb.Mkdir("/sdk", 0755, llb.WithParents(true))).
			File(llb.Copy(build, "/app/src/package.json", "/app/src/package.json")).
			File(llb.Copy(build, "/app/src/yarn.lock", "/app/src/yarn.lock")).
			File(llb.Copy(build, "/sdk/nodejs", "/sdk/nodejs")).
			Run(llb.Shlex(`yarn --production`),
				llb.Dir("/app/src"),
				llb.AddEnv("YARN_CACHE_FOLDER", "/cache/yarn"),
				llb.AddMount(
					"/cache/yarn",
					llb.Scratch(),
					llb.AsPersistentCacheDir("yarn", llb.CacheMountLocked),
				),
			).Root().
			File(llb.Copy(build, "/app/src/dist", "/app/src/")).
			File(llb.Mkfile("/entrypoint", 0755, []byte("#!/bin/sh\nnode --unhandled-rejections=strict /app/src/dist/index.js"))),
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
