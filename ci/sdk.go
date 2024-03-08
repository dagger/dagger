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

func (dg *Dagger) installDagger(ctx context.Context, ctr *dagger.Container, name string) (*dagger.Container, error) {
	ctrs, err := dg.installDaggers(ctx, []*dagger.Container{ctr}, name)
	if err != nil {
		return nil, err
	}
	return ctrs[0], nil
}

func (dg *Dagger) installDaggers(ctx context.Context, ctrs []*dagger.Container, name string) ([]*dagger.Container, error) {
	engineSvc, err := dg.Engine().Service(ctx, name)
	if err != nil {
		return nil, err
	}
	engineEndpoint, err := engineSvc.Endpoint(ctx, ServiceEndpointOpts{Scheme: "tcp"})
	if err != nil {
		return nil, err
	}

	cliBinary, err := dg.CLI().File(ctx)
	if err != nil {
		return nil, err
	}
	cliBinaryPath := "/.dagger-cli"

	for i, ctr := range ctrs {
		ctrs[i] = ctr.
			WithServiceBinding("dagger-engine", engineSvc).
			WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", engineEndpoint).
			WithMountedFile(cliBinaryPath, cliBinary).
			WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", cliBinaryPath).
			WithExec([]string{"ln", "-s", cliBinaryPath, "/usr/local/bin/dagger"})
	}
	return ctrs, nil
}
