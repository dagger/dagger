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
		"Core API"
		core: Core!
	}

	"Core API"
	type Core {
		"Fetch an OCI image"
		image(ref: String!): Filesystem!

		"Fetch a git repository"
		git(remote: String!, ref: String): Filesystem!

		"Fetch a client directory"
		clientdir(id: String!): Filesystem!
	}
	`
}

func (r *coreSchema) Operations() string {
	return `
	query Image($ref: String!) {
		core {
			image(ref: $ref) {
				id
			}
		}
	}
	`
}

func (r *coreSchema) Resolvers() router.Resolvers {
	return router.Resolvers{
		"Query": router.ObjectResolver{
			"core": r.core,
		},
		"Core": router.ObjectResolver{
			"image":     r.image,
			"git":       r.git,
			"clientdir": r.clientdir,
		},
	}
}

func (r *coreSchema) core(p graphql.ResolveParams) (any, error) {
	return struct{}{}, nil
}

func (r *coreSchema) image(p graphql.ResolveParams) (any, error) {
	ref := p.Args["ref"].(string)

	st := llb.Image(ref)
	return r.Solve(p.Context, st)
}

func (r *coreSchema) git(p graphql.ResolveParams) (any, error) {
	remote := p.Args["remote"].(string)
	ref, _ := p.Args["ref"].(string)

	st := llb.Git(remote, ref)
	return r.Solve(p.Context, st)
}

func (r *coreSchema) clientdir(p graphql.ResolveParams) (any, error) {
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
