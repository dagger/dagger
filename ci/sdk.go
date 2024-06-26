package main

import (
	"context"

	"dagger.io/dagger/dag"
	"github.com/dagger/dagger/ci/build"
	"github.com/dagger/dagger/ci/consts"
)

// A dev environment for the official Dagger SDKs
type SDK struct {
	// Develop the Dagger Go SDK
	Go *GoSDK
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
	Generate(ctx context.Context) (*Directory, error)
	Bump(ctx context.Context, version string) (*Directory, error)
}

func (sdk *SDK) allSDKs() []sdkBase {
	return []sdkBase{
		sdk.Go,
		sdk.Python,
		sdk.Typescript,
		sdk.Elixir,
		sdk.Rust,
		sdk.PHP,
		// java isn't properly integrated to our release process yet
		// sdk.Java,
	}
}

func (ci *Dagger) installer(ctx context.Context, name string) (func(*Container) *Container, error) {
	engineSvc, err := ci.Engine().Service(ctx, name, ci.Version)
	if err != nil {
		return nil, err
	}

	cliBinary, err := ci.CLI().File(ctx, "")
	if err != nil {
		return nil, err
	}
	cliBinaryPath := "/.dagger-cli"

	return func(ctr *Container) *Container {
		ctr = ctr.
			WithServiceBinding("dagger-engine", engineSvc).
			WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", "tcp://dagger-engine:1234").
			WithMountedFile(cliBinaryPath, cliBinary).
			WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", cliBinaryPath).
			WithExec([]string{"ln", "-s", cliBinaryPath, "/usr/local/bin/dagger"})
		if ci.HostDockerConfig != nil {
			// this avoids rate limiting in our ci tests
			ctr = ctr.WithMountedSecret("/root/.docker/config.json", ci.HostDockerConfig)
		}
		return ctr
	}, nil
}

func (ci *Dagger) introspection(ctx context.Context, installer func(*Container) *Container) (*File, error) {
	builder, err := build.NewBuilder(ctx, ci.Source)
	if err != nil {
		return nil, err
	}
	return dag.Container().
		From(consts.WolfiImage).
		With(installer).
		WithFile("/usr/local/bin/codegen", builder.CodegenBinary()).
		WithExec([]string{"codegen", "introspect", "-o", "/schema.json"}).
		File("/schema.json"), nil
}
