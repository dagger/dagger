package core

import (
	"fmt"

	"github.com/dagger/cloak/core/filesystem"
	"github.com/dagger/cloak/extension"
	"github.com/dagger/cloak/router"
	"github.com/graphql-go/graphql"
)

type Extension struct {
	Name       string
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
		name: String!
		schema: String!
		operations: String!
	}

	extend type Filesystem {
		loadExtension(name: String!): Extension!
	}

	extend type Core {
		extension(name: String!): Extension!
	}
	`
}

func (s *extensionSchema) Operations() string {
	return ""
}

func (r *extensionSchema) Resolvers() router.Resolvers {
	return router.Resolvers{
		"Filesystem": router.ObjectResolver{
			"loadExtension": r.loadExtension,
		},
		"Core": router.ObjectResolver{
			"extension": r.extension,
		},
	}
}

func (r *extensionSchema) loadExtension(p graphql.ResolveParams) (any, error) {
	obj, err := filesystem.FromSource(p.Source)
	if err != nil {
		return nil, err
	}

	name := p.Args["name"].(string)

	schema, err := extension.Load(p.Context, r.gw, r.platform, obj)
	if err != nil {
		return nil, err
	}

	if err := r.router.Add(name, schema); err != nil {
		return nil, err
	}

	return &Extension{
		Name:       name,
		Schema:     schema.Schema(),
		Operations: schema.Operations(),
	}, nil
}

func (r *extensionSchema) extension(p graphql.ResolveParams) (any, error) {
	name := p.Args["name"].(string)

	schema := r.router.Get(name)
	if schema == nil {
		return nil, fmt.Errorf("extension %q not found", name)
	}

	return &Extension{
		Name:       name,
		Schema:     schema.Schema(),
		Operations: schema.Operations(),
	}, nil
}
