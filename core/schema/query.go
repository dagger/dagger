package schema

import (
	"github.com/dagger/dagger/core"
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
			"group": router.ToResolver(s.group),
		},
	}
}

func (s *querySchema) Dependencies() []router.ExecutableSchema {
	return nil
}

type groupArgs struct {
	Name string
}

func (s *querySchema) group(ctx *router.Context, parent *core.Query, args groupArgs) (*core.Query, error) {
	if parent == nil {
		parent = &core.Query{}
	}
	parent.Context.Group = parent.Context.Group.Add(args.Name)
	return parent, nil
}
