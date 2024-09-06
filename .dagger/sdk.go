package main

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/.dagger/internal/dagger"
)

// A dev environment for the official Dagger SDKs
type SDK struct {
	// Develop the Dagger Python SDK
	Python *PythonSDK
	// Develop the Dagger Typescript SDK
	Typescript *TypescriptSDK

	// Develop the Dagger Elixir SDK (experimental)
	Elixir *ElixirSDK
	// Develop the Dagger Rust SDK (experimental)
	Rust *RustSDK
	// Develop the Dagger PHP SDK (experimental)
	PHP *PHPSDK
	// Develop the Dagger Java SDK (experimental)
	Java *JavaSDK
}

func (sdk *SDK) All() *AllSDK {
	return &AllSDK{
		SDK: sdk,
	}
}

type sdkBase interface {
	Lint(ctx context.Context) error
	Test(ctx context.Context) error
	Generate(ctx context.Context) (*dagger.Directory, error)
	Bump(ctx context.Context, version string) (*dagger.Directory, error)
}

func (sdk *SDK) allSDKs() []sdkBase {
	return []sdkBase{
		sdk.Python,
		sdk.Typescript,
		sdk.Elixir,
		sdk.Rust,
		sdk.PHP,
		// java isn't properly integrated to our release process yet
		// sdk.Java,
	}
}

func (dev *DaggerDev) installer(ctx context.Context, name string) (func(*dagger.Container) *dagger.Container, error) {
	return func(client *dagger.Container) *dagger.Container {
		client, err := dev.Engine().Bind(ctx, client)
		if err != nil {
			panic(err) // installer is a temporary facade
		}
		return client
	}, nil
}

// FIXME: temporary facade
func (dev *DaggerDev) introspection(engine *Engine) *dagger.File {
	return dag.Codegen().Introspect(engine.AsCodegenSidecar)
}

func sdkChangeNotes(src *dagger.Directory, sdk string, version string) *dagger.File {
	return src.File(fmt.Sprintf("sdk/%s/.changes/%s.md", sdk, version))
}
