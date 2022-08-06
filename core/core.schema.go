package core

import (
	"github.com/dagger/cloak/router"
	"github.com/graphql-go/graphql"
	"github.com/moby/buildkit/client/llb"
)

var _ router.ExecutableSchema = &coreSchema{}

type coreSchema struct {
	*baseSchema
}

func (r *coreSchema) Schema() string {
	return `
	extend type Query {
		core: Core!
	}

	type Core {
		image(ref: String!): Filesystem!
		git(remote: String!, ref: String): Filesystem!
	}
	`
}

func (r *coreSchema) Resolvers() router.Resolvers {
	return router.Resolvers{
		"Query": router.ObjectResolver{
			"core": r.Core,
		},
		"Core": router.ObjectResolver{
			"image": r.Image,
		},
	}
}

func (r *coreSchema) Core(p graphql.ResolveParams) (any, error) {
	return struct{}{}, nil
}

func (r *coreSchema) Image(p graphql.ResolveParams) (any, error) {
	ref := p.Args["ref"].(string)

	st := llb.Image(ref)
	return r.Solve(p.Context, st)
}

func (r *coreSchema) Git(p graphql.ResolveParams) (any, error) {
	remote := p.Args["remote"].(string)
	ref, _ := p.Args["ref"].(string)

	st := llb.Git(remote, ref)
	return r.Solve(p.Context, st)
}

var _ router.ExecutableSchema = &coreSchema{}
