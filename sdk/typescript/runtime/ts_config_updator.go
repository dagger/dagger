package main

import (
	"context"
	"fmt"
	"typescript-sdk/internal/dagger"
	"typescript-sdk/tsutils"
)

func CreateOrUpdateTSConfig(ctx context.Context, modSourceDir *dagger.Directory) (*dagger.File, error) {
	tsconfigExist, err := modSourceDir.Glob(ctx, "tsconfig.json")
	if err != nil {
		return nil, fmt.Errorf("failed to lookup for tsconfig.json")
	}

	// If no tsconfig.json is found in the user module, we generate a default one.
	if len(tsconfigExist) == 0 {
		defaultTsConfigContent, err := tsutils.DefaultTSConfigForModule()
		if err != nil {
			return nil, fmt.Errorf("failed to get default tsconfig.json")
		}

		return dag.File("tsconfig.json", defaultTsConfigContent).Sync(ctx)
	}

	tsConfigContent, err := modSourceDir.File("tsconfig.json").Contents(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read module's tsconfig.json")
	}

	updatedTsConfigContent, err := tsutils.UpdateTSConfigForModule(tsConfigContent)
	if err != nil {
		return nil, fmt.Errorf("failed to update tsconfig.json")
	}

	return dag.File("tsconfig.json", updatedTsConfigContent).Sync(ctx)
}
