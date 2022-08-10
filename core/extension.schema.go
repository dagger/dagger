package core

import (
	"github.com/dagger/cloak/core/filesystem"
	"github.com/dagger/cloak/extension"
	"github.com/dagger/cloak/router"
	"github.com/graphql-go/graphql"
)

type Extension struct {
	Schema     string
	Operations string
}

var _ router.ExecutableSchema = &extensionSchema{}

type extensionSchema struct {
	*baseSchema
}

func (s *extensionSchema) Schema() string {
	return `
	type Extension {
		schema: String!
		operations: String!
	}

	extend type Filesystem {
		loadExtension: Extension!
	}
	`
}

func (s *extensionSchema) Operations() string {
	return ""
}

func (r *extensionSchema) Resolvers() router.Resolvers {
	return router.Resolvers{
		"Filesystem": router.ObjectResolver{
			"loadExtension": r.LoadExtension,
		},
	}
}

func (r *extensionSchema) LoadExtension(p graphql.ResolveParams) (any, error) {
	obj, err := filesystem.FromSource(p.Source)
	if err != nil {
		return nil, err
	}

	schema, err := extension.Load(p.Context, r.gw, r.platform, obj)
	if err != nil {
		return nil, err
	}

	if err := r.router.Add(schema); err != nil {
		return nil, err
	}

	return &Extension{
		Schema:     schema.Schema(),
		Operations: schema.Operations(),
	}, nil
}
