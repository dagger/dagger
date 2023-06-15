package schema

import (
	"github.com/blang/semver"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/pipeline"
	"github.com/dagger/dagger/internal/engine"
	"github.com/dagger/dagger/router"
)

type querySchema struct {
	*baseSchema
}

var _ router.ExecutableSchema = &querySchema{}

func (s *querySchema) Name() string {
	return "query"
}

func (s *querySchema) Schema() string {
	return Query
}

func (s *querySchema) Resolvers() router.Resolvers {
	return router.Resolvers{
		"Query": router.ObjectResolver{
			"pipeline": router.ToResolver(s.pipeline),
		},
	}
}

func (s *querySchema) Dependencies() []router.ExecutableSchema {
	return nil
}

type pipelineArgs struct {
	Name        string
	Description string
	Labels      []pipeline.Label
}

func (s *querySchema) pipeline(ctx *router.Context, parent *core.Query, args pipelineArgs) (*core.Query, error) {
	if parent == nil {
		parent = &core.Query{}
	}
	parent.Context.Pipeline = parent.Context.Pipeline.Add(pipeline.Pipeline{
		Name:        args.Name,
		Description: args.Description,
		Labels:      args.Labels,
	})
	return parent, nil
}

type checkVersionCompatibilityArgs struct {
	Version string
}

func (s *querySchema) checkVersionCompatibility(_ *router.Context, _ *core.Query, args checkVersionCompatibilityArgs) (bool, error) {
	engineVersion, err := semver.Parse(engine.Version)
	if err != nil {
		return false, err
	}

	sdkVersion, err := semver.Parse(args.Version)
	if err != nil {
		return false, err
	}

	if engineVersion.Major != sdkVersion.Major || engineVersion.Minor != sdkVersion.Minor {
		return false, nil
	}

	return true, nil
}
