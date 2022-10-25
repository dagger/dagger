package project

import (
	"context"
	"path/filepath"

	"github.com/containerd/containerd/platforms"
	"github.com/dagger/dagger/core"
	dockerfilebuilder "github.com/moby/buildkit/frontend/dockerfile/builder"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

func (p *State) dockerfileRuntime(ctx context.Context, subpath string, gw bkgw.Client, platform specs.Platform) (*core.Directory, error) {
	// TODO(vito): handle relative path + platform?
	payload, err := p.workdir.ID.Decode()
	if err != nil {
		return nil, err
	}

	opts := map[string]string{
		"platform": platforms.Format(platform),
		"filename": filepath.ToSlash(filepath.Join(filepath.Dir(p.configPath), subpath, "Dockerfile")),
	}
	inputs := map[string]*pb.Definition{
		dockerfilebuilder.DefaultLocalNameContext:    payload.LLB,
		dockerfilebuilder.DefaultLocalNameDockerfile: payload.LLB,
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

	newSt, err := bkref.ToState()
	if err != nil {
		return nil, err
	}

	return core.NewDirectory(ctx, newSt, "", platform)
}
