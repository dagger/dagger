package main

import (
	"context"
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

func (ci *Dagger) installer(ctx context.Context, name string) (func(*Container) *Container, error) {
	engineSvc, err := ci.Engine().Service(ctx, name)
	if err != nil {
		return nil, err
	}
	engineEndpoint, err := engineSvc.Endpoint(ctx, ServiceEndpointOpts{Scheme: "tcp"})
	if err != nil {
		return nil, err
	}

	cliBinary, err := ci.CLI().File(ctx, "")
	if err != nil {
		return nil, err
	}
	cliBinaryPath := "/.dagger-cli"

	return func(ctr *Container) *Container {
		return ctr.
			WithServiceBinding("dagger-engine", engineSvc).
			WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", engineEndpoint).
			WithMountedFile(cliBinaryPath, cliBinary).
			WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", cliBinaryPath).
			WithExec([]string{"ln", "-s", cliBinaryPath, "/usr/local/bin/dagger"})
	}, nil
}
