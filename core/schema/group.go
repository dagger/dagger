package schema

import (
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/router"
)

type groupSchema struct {
	*baseSchema
}

var _ router.ExecutableSchema = &groupSchema{}

func (s *groupSchema) Name() string {
	return "group"
}

func (s *groupSchema) Schema() string {
	return Group
}

func (s *groupSchema) Resolvers() router.Resolvers {
	return router.Resolvers{
		"Query": router.ObjectResolver{
			"group": router.ToResolver(s.group),
		},
	}
}

func (s *groupSchema) Dependencies() []router.ExecutableSchema {
	return nil
}

type groupArgs struct {
	Name string
}

func (s *groupSchema) group(ctx *router.Context, parent *core.Query, args groupArgs) (*core.Query, error) {
	if parent == nil {
		parent = &core.Query{}
	}
	parent.Context.Group = parent.Context.Group.Add(args.Name)
	return parent, nil
}
