package main

import (
	"context"
	"encoding/json"
	"fmt"

	"elixir-sdk/internal/dagger"
	"elixir-sdk/internal/names"
)

// elixirNameFormats are the name formats the Elixir codegen uses: see
// Dagger.Codegen.Naming.formats/0.
var elixirNameFormats = []names.Format{
	{Casing: "PASCAL", Acronyms: "UPPERCASE"},
	{Casing: "SNAKE", Acronyms: "UPPERCASE"},
}

// formattedNamesFile formats the names of the module schema in
// introspectionJSON for the Elixir codegen, whose exec has no engine session,
// as a sidecar file for its --names. It returns nil for a schema without
// Query.formatIdentifiers, so codegen keeps its legacy conversion.
func formattedNamesFile(ctx context.Context, introspectionJSON *dagger.File) (*dagger.File, error) {
	schemaJSON, err := introspectionJSON.Contents(ctx)
	if err != nil {
		return nil, fmt.Errorf("read introspection JSON: %w", err)
	}
	file, err := names.File(ctx, []byte(schemaJSON), elixirNameFormats, names.Engine(dag.GraphQLClient()))
	if err != nil {
		return nil, err
	}
	if file == nil {
		return nil, nil
	}
	contents, err := json.Marshal(file)
	if err != nil {
		return nil, err
	}
	return dag.Directory().WithNewFile("names.json", string(contents)).File("names.json"), nil
}
