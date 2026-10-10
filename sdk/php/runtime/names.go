package main

import (
	"context"
	"encoding/json"
	"fmt"

	"php-sdk/internal/dagger"
	"php-sdk/internal/names"
)

// phpNameFormats are the name formats the PHP codegen uses: see
// NewCodegenVisitor::NAME_FORMATS.
var phpNameFormats = []names.Format{
	{Casing: string(dagger.CasingCamel), Acronyms: string(dagger.AcronymStyleCapitalized)},
	{Casing: string(dagger.CasingScreamingSnake), Acronyms: string(dagger.AcronymStyleUppercase)},
}

// formattedNamesFile formats the names of the module schema in
// introspectionJSON for the PHP codegen, whose exec has no engine session, as
// a sidecar file for its --names-file. It returns nil for a schema without
// Query.formatIdentifiers, so codegen keeps its legacy conversion.
func formattedNamesFile(ctx context.Context, introspectionJSON *dagger.File) (*dagger.File, error) {
	schemaJSON, err := introspectionJSON.Contents(ctx)
	if err != nil {
		return nil, fmt.Errorf("read introspection JSON: %w", err)
	}
	file, err := names.File(ctx, []byte(schemaJSON), phpNameFormats, formatIdentifiers)
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

// formatIdentifiers formats names with Query.formatIdentifiers, parsing them
// with the naming dictionary of version.
func formatIdentifiers(ctx context.Context, batch []string, format names.Format, version string) ([]string, error) {
	return dag.FormatIdentifiers(ctx, batch, dagger.Casing(format.Casing), dagger.FormatIdentifiersOpts{
		Acronyms: dagger.AcronymStyle(format.Acronyms),
		Version:  version,
	})
}
