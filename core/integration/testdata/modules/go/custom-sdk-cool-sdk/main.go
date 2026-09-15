package main

import (
	"context"
	"encoding/json"

	"dagger/cool-sdk/internal/dagger"
)

type CoolSdk struct{}

func (m *CoolSdk) ModuleTypes(ctx context.Context, modSource *dagger.ModuleSource, introspectionJSON *dagger.File, outputFilePath string) (*dagger.Container, error) {
	mod := modSource.WithSDK("go").AsModule()
	modID, err := mod.ID(ctx)
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(modID)
	if err != nil {
		return nil, err
	}
	return dag.Container().
		From("alpine").
		WithNewFile(outputFilePath, string(b)).
		WithEntrypoint([]string{
			"sh", "-c", "",
		}), nil
}

func (m *CoolSdk) ModuleRuntime(ctx context.Context, modSource *dagger.ModuleSource, introspectionJson *dagger.File) (*dagger.Container, error) {
	runtime, err := modSource.WithSDK("go").AsModule().Runtime(ctx)
	if err != nil || runtime == nil {
		return runtime, err
	}
	return runtime.WithEnvVariable("COOL", "true"), nil
}

func (m *CoolSdk) Codegen(modSource *dagger.ModuleSource, introspectionJson *dagger.File) *dagger.GeneratedCode {
	return dag.GeneratedCode(modSource.WithSDK("go").AsModule().GeneratedContextDirectory())
}
