package main

import (
	"context"
	"fmt"
	"typescript-sdk/internal/dagger"
	"typescript-sdk/tsutils"
)

/////
// tsconfig.json update function
/////

func CreateOrUpdateTSConfigForModule(ctx context.Context, modSourceDir *dagger.Directory) (*dagger.File, error) {
	tsconfigExist, err := modSourceDir.Exists(ctx, "tsconfig.json")
	if err != nil {
		return nil, fmt.Errorf("failed to lookup for tsconfig.json: %w", err)
	}

	// If no tsconfig.json is found in the user module, we generate a default one.
	if !tsconfigExist {
		defaultTSConfigContent := tsutils.DefaultTSConfig()

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

func CreateOrUpdateTSConfigForClient(ctx context.Context, modSourceDir *dagger.Directory, isRemote bool) (*dagger.File, error) {
	tsConfigExist, err := modSourceDir.Exists(ctx, "tsconfig.json")
	if err != nil {
		return nil, fmt.Errorf("failed to lookup for tsconfig.json: %w", err)
	}

	if !tsConfigExist {
		defaultTSConfigContent := tsutils.DefaultTSConfig()
		return dag.File("tsconfig.json", defaultTSConfigContent).Sync(ctx)
	}

	tsConfigContent, err := modSourceDir.File("tsconfig.json").Contents(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read module's tsconfig.json: %w", err)
	}

	updatedTSConfigContent, err := tsutils.UpdateTSConfigForClient(tsConfigContent, isRemote)
	if err != nil {
		return nil, fmt.Errorf("failed to update tsconfig.json: %w", err)
	}

	return dag.File("tsconfig.json", updatedTSConfigContent).Sync(ctx)
}

/////
// package.json update functions
/////

func CreateOrUpdatePackageJSONForModule(ctx context.Context, file *dagger.File) (*dagger.File, error) {
	packageJSON, err := file.Contents(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read package.json: %w", err)
	}

	packageJSON, err = tsutils.UpdatePackageJSONForModule(packageJSON)
	if err != nil {
		return nil, err
	}

	return dag.File("package.json", packageJSON).Sync(ctx)
}

func CreateOrUpdatePackageJSONForClient(ctx context.Context, file *dagger.File) (*dagger.File, error) {
	packageJSON, err := file.Contents(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read package.json: %w", err)
	}

	packageJSON, err = tsutils.UpdatePackageJSONForClient(packageJSON)
	if err != nil {
		return nil, err
	}

	return dag.File("package.json", packageJSON).Sync(ctx)
}

/////
// deno.json update function
/////

func UpdateDenoJSONForModule(ctx context.Context, file *dagger.File) (*dagger.File, error) {
	denoJSON, err := file.Contents(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read deno.json: %w", err)
	}

	denoJSON, err = tsutils.UpdateDenoConfigForModule(denoJSON)
	if err != nil {
		return nil, err
	}

	return dag.File("deno.json", denoJSON).Sync(ctx)
}

func UpdateDenoJSONForClient(ctx context.Context, file *dagger.File, isRemote bool) (*dagger.File, error) {
	denoJSON, err := file.Contents(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read deno.json: %w", err)
	}

	denoJSON, err = tsutils.UpdateDenoConfigForClient(denoJSON, isRemote)
	if err != nil {
		return nil, err
	}

	return dag.File("deno.json", denoJSON).Sync(ctx)
}
