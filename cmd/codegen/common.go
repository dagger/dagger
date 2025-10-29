package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"dagger.io/dagger"
	"github.com/dagger/dagger/cmd/codegen/generator"
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

	// If a module source ID is provided or no introspection JSON is provided, we will query
	// the engine so we can create a connection here.
	if moduleSourceID != "" || introspectionJSONPath == "" || alwaysConnect {
		dag, err := dagger.Connect(ctx)
		if err != nil {
			return generator.Config{}, fmt.Errorf("failed to connect to engine: %w", err)
		}

		cfg.Dag = dag
	}

	if introspectionJSONPath != "" {
		introspectionJSON, err := os.ReadFile(introspectionJSONPath)
		if err != nil {
			return generator.Config{}, fmt.Errorf("read introspection json: %w", err)
		}
		cfg.IntrospectionJSON = string(introspectionJSON)
	}

	return cfg, nil
}
