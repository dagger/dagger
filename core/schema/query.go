package schema

import (
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/pipeline"
)

type querySchema struct {
	*MergedSchemas
}

var _ ExecutableSchema = &querySchema{}

func (s *querySchema) Name() string {
	return "query"
}

func (s *querySchema) Schema() string {
	return Query
}

func (s *querySchema) Resolvers() Resolvers {
	return Resolvers{
		"Query": ObjectResolver{
			"pipeline": ToResolver(s.pipeline),
		},
	}
}

func (s *querySchema) Dependencies() []ExecutableSchema {
	return nil
}

type pipelineArgs struct {
	Name        string
	Description string
	Labels      []pipeline.Label
}

func (s *querySchema) pipeline(ctx *core.Context, parent *core.Query, args pipelineArgs) (*core.Query, error) {
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
