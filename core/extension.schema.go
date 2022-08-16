package core

import (
	"fmt"
	"sync"

	"github.com/dagger/cloak/core/filesystem"
	"github.com/dagger/cloak/extension"
	"github.com/dagger/cloak/router"
	"github.com/graphql-go/graphql"
	"golang.org/x/sync/singleflight"
)

type Extension struct {
	Name         string
	Schema       string
	Operations   string
	Dependencies []*Extension
	schema       *extension.RemoteSchema // internal-only, for convenience in `install` resolver
}

var _ router.ExecutableSchema = &extensionSchema{}

type extensionSchema struct {
	*baseSchema
	compiledSchemas map[string]*extension.CompiledRemoteSchema
	l               sync.RWMutex
	sf              singleflight.Group
}

func (s *extensionSchema) Name() string {
	return "extension"
}

func (s *extensionSchema) Schema() string {
	return `
	"Extension representation"
	type Extension {
		"name of the extension"
		name: String!

		"schema of the extension"
		schema: String

		"operations for this extension"
		operations: String

		"dependencies for this extension"
		dependencies: [Extension!]

		"install the extension, stitching its schema into the API"
		install: Filesystem!
	}

	extend type Filesystem {
		"load an extension into the API"
		loadExtension(configPath: String!): Extension!
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
		"Extension": router.ObjectResolver{
			"install": s.install,
		},
	}
}

func (s *extensionSchema) Dependencies() []router.ExecutableSchema {
	return nil
}

func (s *extensionSchema) install(p graphql.ResolveParams) (any, error) {
	obj := p.Source.(*Extension)

	executableSchema, err := obj.schema.Compile(p.Context, s.compiledSchemas, &s.l, &s.sf)
	if err != nil {
		return nil, err
	}

	if err := s.router.Add(executableSchema); err != nil {
		return nil, err
	}

	return executableSchema.RuntimeFS(), nil
}

func (s *extensionSchema) loadExtension(p graphql.ResolveParams) (any, error) {
	obj, err := filesystem.FromSource(p.Source)
	if err != nil {
		return nil, err
	}

	configPath := p.Args["configPath"].(string)
	schema, err := extension.Load(p.Context, s.gw, s.platform, obj, configPath)
	if err != nil {
		return nil, err
	}

	return remoteSchemaToExtension(schema), nil
}

func (s *extensionSchema) extension(p graphql.ResolveParams) (any, error) {
	name := p.Args["name"].(string)

	schema := s.router.Get(name)
	if schema == nil {
		return nil, fmt.Errorf("extension %q not found", name)
	}

	return routerSchemaToExtension(schema), nil
}

// TODO:(sipsma) guard against infinite recursion
func routerSchemaToExtension(schema router.ExecutableSchema) *Extension {
	ext := &Extension{
		Name:       schema.Name(),
		Schema:     schema.Schema(),
		Operations: schema.Operations(),
	}
	for _, dep := range schema.Dependencies() {
		ext.Dependencies = append(ext.Dependencies, routerSchemaToExtension(dep))
	}
	return ext
}

// TODO:(sipsma) guard against infinite recursion
func remoteSchemaToExtension(schema *extension.RemoteSchema) *Extension {
	ext := &Extension{
		Name:       schema.Name(),
		Schema:     schema.Schema(),
		Operations: schema.Operations(),
		schema:     schema,
	}
	for _, dep := range schema.Dependencies() {
		ext.Dependencies = append(ext.Dependencies, remoteSchemaToExtension(dep))
	}
	return ext
}
