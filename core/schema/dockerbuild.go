package schema

import (
	"github.com/containerd/containerd/platforms"
	dockerfilebuilder "github.com/moby/buildkit/frontend/dockerfile/builder"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
	"go.dagger.io/dagger/core/filesystem"
	"go.dagger.io/dagger/router"
)

var _ router.ExecutableSchema = &dockerBuildSchema{}

type dockerBuildSchema struct {
	*baseSchema
}

func (s *dockerBuildSchema) Name() string {
	return "dockerbuild"
}

func (s *dockerBuildSchema) Schema() string {
	return `
extend type Filesystem {
	"docker build using this filesystem as context"
	dockerbuild(dockerfile: String): Filesystem!
}
	`
}

func (s *dockerBuildSchema) Resolvers() router.Resolvers {
	return router.Resolvers{
		"Filesystem": router.ObjectResolver{
			"dockerbuild": router.ToResolver(s.dockerbuild),
		},
	}
}

func (s *dockerBuildSchema) Dependencies() []router.ExecutableSchema {
	return nil
}

type dockerbuildArgs struct {
	Dockerfile string
}

func (s *dockerBuildSchema) dockerbuild(ctx *router.Context, parent *filesystem.Filesystem, args dockerbuildArgs) (any, error) {
	def, err := parent.ToDefinition()
	if err != nil {
		return nil, err
	}

	opts := map[string]string{
		"platform": platforms.Format(s.platform),
	}
	if dockerfile := args.Dockerfile; dockerfile != "" {
		opts["filename"] = dockerfile
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
