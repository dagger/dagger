package core

import "github.com/dagger/cloak/router"

type rootSchema struct {
	*baseSchema
}

func (r *rootSchema) Schema() string {
	return `
	type Query {
	}
	`
}

func (r *rootSchema) Resolvers() router.Resolvers {
	return router.Resolvers{}
}
