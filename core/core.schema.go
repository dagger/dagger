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
		clientdir(id: String!): Filesystem!
	}
	`
}

func (r *coreSchema) Resolvers() router.Resolvers {
	return router.Resolvers{
		"Query": router.ObjectResolver{
			"core": r.Core,
		},
		"Core": router.ObjectResolver{
			"image":     r.Image,
			"git":       r.Git,
			"clientdir": r.ClientDir,
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

func (r *coreSchema) ClientDir(p graphql.ResolveParams) (any, error) {
	id := p.Args["id"].(string)

	// copy to scratch to avoid making buildkit's snapshot of the local dir immutable,
	// which makes it unable to reused, which in turn creates cache invalidations
	// TODO: this should be optional, the above issue can also be avoided w/ readonly
	// mount when possible
	st := llb.Scratch().File(llb.Copy(llb.Local(
		id,
		// TODO: better shared key hint?
		llb.SharedKeyHint(id),
		// FIXME: should not be hardcoded
		llb.ExcludePatterns([]string{"**/node_modules"}),
	), "/", "/"))

	return r.Solve(p.Context, st, llb.LocalUniqueID(id))
}
