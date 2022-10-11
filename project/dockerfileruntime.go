package project

import (
	"context"
	"path/filepath"

	"github.com/containerd/containerd/platforms"
	"github.com/moby/buildkit/client/llb"
	dockerfilebuilder "github.com/moby/buildkit/frontend/dockerfile/builder"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"go.dagger.io/dagger/core"
)

func (p *State) dockerfileRuntime(ctx context.Context, subpath string, gw bkgw.Client, platform specs.Platform) (*core.Directory, error) {
	st, _, platform, err := p.workdir.Decode() // TODO(vito): handle relative path?
	if err != nil {
		return nil, err
	}

	def, err := st.Marshal(ctx, llb.Platform(platform))
	if err != nil {
		return nil, err
	}

	opts := map[string]string{
		"platform": platforms.Format(platform),
		"filename": filepath.ToSlash(filepath.Join(filepath.Dir(p.configPath), subpath, "Dockerfile")),
	}
	inputs := map[string]*pb.Definition{
		dockerfilebuilder.DefaultLocalNameContext:    def.ToPB(),
		dockerfilebuilder.DefaultLocalNameDockerfile: def.ToPB(),
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
