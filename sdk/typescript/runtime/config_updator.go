package main

import (
	"context"
	"fmt"
	"typescript-sdk/internal/dagger"
	"typescript-sdk/tsutils"
)

func CreateOrUpdateTSConfigForModule(ctx context.Context, modSourceDir *dagger.Directory) (*dagger.File, error) {
	tsconfigExist, err := modSourceDir.Exists(ctx, "tsconfig.json")
	if err != nil {
		return nil, fmt.Errorf("failed to lookup for tsconfig.json: %w", err)
	}

	// If no tsconfig.json is found in the user module, we generate a default one.
	if !tsconfigExist {
		defaultTSConfigContent := tsutils.DefaultTSConfigForModule()

		return dag.File("tsconfig.json", defaultTSConfigContent).Sync(ctx)
	}

	tsConfigContent, err := modSourceDir.File("tsconfig.json").Contents(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read module's tsconfig.json: %w", err)
	}

	updatedTSConfigContent, err := tsutils.UpdateTSConfigForModule(tsConfigContent)
	if err != nil {
		return nil, fmt.Errorf("failed to update tsconfig.json: %w", err)
	}

	return dag.File("tsconfig.json", updatedTSConfigContent).Sync(ctx)
}

func CreateOrUpdatePackageJSON(ctx context.Context, file *dagger.File) (*dagger.File, error) {
	packageJSON, err := file.Contents(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read package.json: %w", err)
	}

	packageJSON, err = tsutils.UpdatePackageJSON(packageJSON)
	if err != nil {
		return nil, err
	}

	return dag.File("package.json", packageJSON).Sync(ctx)
}
