package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"dagger.io/dagger"
	"github.com/dagger/dagger/cmd/codegen/generator"
	"github.com/dagger/dagger/cmd/codegen/introspection"
)

var (
	outputDir             string
	lang                  string
	introspectionJSONPath string
	bundle                bool
)

func relativeTo(basepath string, tarpath string) (string, error) {
	basepath, err := filepath.Abs(basepath)
	if err != nil {
		return "", err
	}
	tarpath, err = filepath.Abs(tarpath)
	if err != nil {
		return "", err
	}
	return filepath.Rel(basepath, tarpath)
}

func getGlobalConfig(ctx context.Context, alwaysConnect bool) (generator.Config, error) {
	cfg := generator.Config{
		Lang:      generator.SDKLang(lang),
		OutputDir: outputDir,
		Bundle:    bundle,
	}

	var introspectionJSON []byte
	if introspectionJSONPath != "" {
		var err error
		introspectionJSON, err = os.ReadFile(introspectionJSONPath)
		if err != nil {
			return generator.Config{}, fmt.Errorf("read introspection json: %w", err)
		}
		cfg.IntrospectionJSON = string(introspectionJSON)
	}

	// If a module source ID is provided or no introspection JSON is provided, we will query
	// the engine so we can create a connection here. Generating from a schema
	// with Query.formatIdentifiers needs one too: codegen asks the engine to
	// format the schema's names.
	if moduleSourceID != "" || introspectionJSONPath == "" || alwaysConnect || formatsNames(introspectionJSON) {
		dag, err := dagger.Connect(ctx)
		if err != nil {
			return generator.Config{}, fmt.Errorf("failed to connect to engine: %w", err)
		}

		cfg.Dag = dag
	}

	return cfg, nil
}

// formatsNames reports whether codegen formats the names of the schema in
// introspectionJSON through the engine (see introspection.FormatNames).
func formatsNames(introspectionJSON []byte) bool {
	if len(introspectionJSON) == 0 {
		return false
	}
	var resp introspection.Response
	if err := json.Unmarshal(introspectionJSON, &resp); err != nil {
		// Generate reports the error.
		return false
	}
	return resp.Schema.HasFormatIdentifiers()
}
