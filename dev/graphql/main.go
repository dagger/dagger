package main

import (
	"github.com/dagger/dagger/dev/graphql/internal/dagger"
)

// An instance of the GraphQL module
type Graphql struct{}

// A GraphQL schema
type Schema struct {
	// The schema encoded to a .graphql file
	File *dagger.File
}

// Load a GraphQL schema from a JSON spec
func (m *Graphql) FromJSON(spec *dagger.File) *Schema {
	return &Schema{
		File: dag.
			Container().
			From("node:16-alpine").
			WithExec([]string{"npm", "install", "-g", "graphql-json-to-sdl"}).
			WithMountedFile("/src/schema.json", spec).
			WithExec([]string{"graphql-json-to-sdl", "/src/schema.json", "/src/schema.graphql"}).
			File("/src/schema.graphql"),
	}
}
