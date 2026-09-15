package main

import (
	"context"
	"encoding/json"

	"dagger/coolsdk/internal/dagger"
)

const daggerJSONGoSDK = `{
	"name": "foo",
	"engineVersion": "v0.16.2",
	"sdk": {
		"source": "go"
	},
	"source": ".dagger"
}`

type Coolsdk struct {
	BarConfig string
}

func New(
	// +default="class-default"
	barConfig string,
) *Coolsdk {
	return &Coolsdk{
		BarConfig: barConfig,
	}
}

func (m *Coolsdk) WithDaggerJson(modSource *dagger.ModuleSource) *dagger.ModuleSource {
	return modSource.ContextDirectory().
		WithNewFile("dagger.json", daggerJSONGoSDK).
		AsModuleSource()
}

func (m *Coolsdk) ModuleTypes(ctx context.Context, modSource *dagger.ModuleSource, introspectionJSON *dagger.File, outputFilePath string) (*dagger.Container, error) {
	mod := m.WithDaggerJson(modSource).WithSDK("go").AsModule()
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

func (m *Coolsdk) ModuleRuntime(ctx context.Context, modSource *dagger.ModuleSource, introspectionJson *dagger.File) (*dagger.Container, error) {
	runtime, err := m.WithDaggerJson(modSource).WithSDK("go").AsModule().Runtime(ctx)
	if err != nil || runtime == nil {
		return runtime, err
	}
	return runtime.WithEnvVariable("COOL", m.BarConfig), nil
}

func (m *Coolsdk) Codegen(modSource *dagger.ModuleSource, introspectionJson *dagger.File) *dagger.GeneratedCode {
	modSource = m.WithDaggerJson(modSource).WithSDK("go")
	return dag.GeneratedCode(
		modSource.ContextDirectory().WithDirectory("/", modSource.GeneratedContextDirectory()),
	)
}
