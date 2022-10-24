package shim

import (
	"context"
	"embed"
	"io/fs"
	"path"
	"sync"

	"github.com/containerd/containerd/platforms"
	"github.com/moby/buildkit/client/llb"
	dockerfilebuilder "github.com/moby/buildkit/frontend/dockerfile/builder"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

//go:embed cmd/*
var cmd embed.FS

var (
	state llb.State
	lock  sync.Mutex
)

const Path = "/_shim"

func init() {
	entries, err := fs.ReadDir(cmd, "cmd")
	if err != nil {
		panic(err)
	}

	state = llb.Scratch()
	for _, e := range entries {
		contents, err := fs.ReadFile(cmd, path.Join("cmd", e.Name()))
		if err != nil {
			panic(err)
		}

		state = state.File(llb.Mkfile(e.Name(), e.Type().Perm(), contents))
		e.Name()
	}
}

func Build(ctx context.Context, gw bkgw.Client, p specs.Platform, cacheImports []bkgw.CacheOptionsEntry) (llb.State, error) {
	lock.Lock()
	def, err := state.Marshal(ctx, llb.Platform(p))
	lock.Unlock()
	if err != nil {
		return llb.State{}, err
	}

	opts := map[string]string{
		"platform": platforms.Format(p),
	}
	inputs := map[string]*pb.Definition{
		dockerfilebuilder.DefaultLocalNameContext:    def.ToPB(),
		dockerfilebuilder.DefaultLocalNameDockerfile: def.ToPB(),
	}
	res, err := gw.Solve(ctx, bkgw.SolveRequest{
		Frontend:       "dockerfile.v0",
		FrontendOpt:    opts,
		FrontendInputs: inputs,
		CacheImports:   cacheImports,
	})
	if err != nil {
		return llb.State{}, err
	}

	bkref, err := res.SingleRef()
	if err != nil {
		return llb.State{}, err
	}

	return bkref.ToState()
}
