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
	"Extension representation"
	type Extension {
		"name of the extension"
		name: String!

		"schema of the extension"
		schema: String!

		"operations for this extension"
		operations: String!
	}

	extend type Filesystem {
		"load an extension into the API"
		loadExtension(name: String!): Extension!
	}

	extend type Core {
		"Look up an extension by name"
		extension(name: String!): Extension!
	}
	`
}

func (s *extensionSchema) Operations() string {
	return ""
}

func (s *extensionSchema) Resolvers() router.Resolvers {
	return router.Resolvers{
		"Filesystem": router.ObjectResolver{
			"loadExtension": s.loadExtension,
		},
		"Core": router.ObjectResolver{
			"extension": s.extension,
		},
	}
}

func (s *extensionSchema) loadExtension(p graphql.ResolveParams) (any, error) {
	obj, err := filesystem.FromSource(p.Source)
	if err != nil {
		return nil, err
	}

	name := p.Args["name"].(string)

	schema, err := extension.Load(p.Context, s.gw, s.platform, obj)
	if err != nil {
		return nil, err
	}

	if err := s.router.Add(name, schema); err != nil {
		return nil, err
	}

	return &Extension{
		Name:       name,
		Schema:     schema.Schema(),
		Operations: schema.Operations(),
	}, nil
}

func (s *extensionSchema) extension(p graphql.ResolveParams) (any, error) {
	name := p.Args["name"].(string)

	schema := s.router.Get(name)
	if schema == nil {
		return nil, fmt.Errorf("extension %q not found", name)
	}

	return &Extension{
		Name:       name,
		Schema:     schema.Schema(),
		Operations: schema.Operations(),
	}, nil
}
