package project

import (
	"context"
	"path/filepath"

	"github.com/containerd/containerd/platforms"
	"github.com/dagger/cloak/core/filesystem"
	dockerfilebuilder "github.com/moby/buildkit/frontend/dockerfile/builder"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
)

func (s RemoteSchema) dockerfileRuntime(ctx context.Context, subpath string) (*filesystem.Filesystem, error) {
	def, err := s.contextFS.ToDefinition()
	if err != nil {
		return nil, err
	}

	opts := map[string]string{
		"platform": platforms.Format(s.platform),
		"filename": filepath.Join(filepath.Dir(s.configPath), subpath, "Dockerfile"),
	}
	inputs := map[string]*pb.Definition{
		dockerfilebuilder.DefaultLocalNameContext:    def,
		dockerfilebuilder.DefaultLocalNameDockerfile: def,
	}
	res, err := s.gw.Solve(ctx, bkgw.SolveRequest{
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

	return filesystem.FromState(ctx, st, s.platform)
}
