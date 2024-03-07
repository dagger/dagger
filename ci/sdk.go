package main

import (
	"context"
	"dagger/internal/dagger"
)

type SDK struct {
	Go         *GoSDK
	Python     *PythonSDK
	Typescript *TypescriptSDK

	Elixir *ElixirSDK
	Rust   *RustSDK
	Java   *JavaSDK
	PHP    *PHPSDK
}

func (dagger *Dagger) installDagger(ctx context.Context, ctr *dagger.Container, name string) (*dagger.Container, error) {
	engineSvc, err := dagger.Engine().Service(ctx, name)
	if err != nil {
		return nil, err
	}
	engineEndpoint, err := engineSvc.Endpoint(ctx, ServiceEndpointOpts{Scheme: "tcp"})
	if err != nil {
		return nil, err
	}

	cliBinary, err := dagger.CLI().File(ctx)
	if err != nil {
		return nil, err
	}
	cliBinaryPath := "/.dagger-cli"

	ctr = ctr.
		WithServiceBinding("dagger-engine", engineSvc).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", engineEndpoint).
		WithMountedFile(cliBinaryPath, cliBinary).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", cliBinaryPath)
	return ctr, nil
}
