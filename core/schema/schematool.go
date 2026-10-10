package schema

import (
	"context"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

// schemaToolsSchema exposes the schema-merge tool that operates on GraphQL
// introspection JSON. It is provided by the engine so that every SDK merges
// schemas through the exact same implementation, rather than reimplementing it
// in each language.
type schemaToolsSchema struct{}

var _ SchemaResolvers = &schemaToolsSchema{}

func (s *schemaToolsSchema) Install(srv *dagql.Server) {
	dagql.Fields[*core.Query]{
		dagql.Func("schema", s.schema).
			View(AfterVersion("v1.0.0-0")).
			Doc(`Load a GraphQL introspection schema for merging.`).
			Args(
				dagql.Arg("json").Doc(`The introspection schema JSON to load.`),
			),
	}.Install(srv)

	srv.InstallObject(dagql.NewClass[*core.Schema](srv).View(AfterVersion("v1.0.0-0")))

	dagql.Fields[*core.Schema]{
		dagql.Func("contents", s.contents).
			Doc(`Serialize the schema back to introspection JSON.`),
		dagql.Func("merge", s.merge).
			Doc(`Merge a module's introspection-shaped type definitions into the schema, returning the combined schema.`).
			Args(
				dagql.Arg("moduleTypes").Doc(`Introspection JSON describing the types the module defines. Object, interface and enum types are appended to the schema, and a constructor field for the module is added to the Query type.`),
				dagql.Arg("moduleName").Doc(`The name of the module whose types are being merged. Used to stamp the @sourceMap directive and to derive the module's constructor field.`),
			),
	}.Install(srv)
}

func (s *schemaToolsSchema) schema(ctx context.Context, _ *core.Query, args struct {
	JSON core.JSON `name:"json"`
}) (*core.Schema, error) {
	return core.NewSchema(args.JSON)
}

func (s *schemaToolsSchema) contents(ctx context.Context, self *core.Schema, _ struct{}) (core.JSON, error) {
	return self.Contents()
}

func (s *schemaToolsSchema) merge(ctx context.Context, self *core.Schema, args struct {
	ModuleTypes core.JSON
	ModuleName  string
}) (*core.Schema, error) {
	return self.Merge(args.ModuleTypes, args.ModuleName)
}
