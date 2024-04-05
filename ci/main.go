package main

import (
	"context"

	"github.com/dagger/dagger/ci/internal/dagger"
	"github.com/dagger/dagger/ci/util"
)

type Dagger struct {
	Source *Directory // +private
}

func New(source *Directory) *Dagger {
	return &Dagger{
		Source: source,
	}
}

func (ci *Dagger) CLI() *CLI {
	return &CLI{Dagger: ci}
}

func (ci *Dagger) Engine() *Engine {
	return &Engine{Dagger: ci}
}

func (ci *Dagger) Docs() *Docs {
	return &Docs{Dagger: ci}
}

func (ci *Dagger) Test() *Test {
	return &Test{Dagger: ci}
}

func (ci *Dagger) SDK() *SDK {
	return &SDK{
		Go:         &GoSDK{Dagger: ci},
		Python:     &PythonSDK{Dagger: ci},
		Typescript: &TypescriptSDK{Dagger: ci},
		Rust:       &RustSDK{Dagger: ci},
		Elixir:     &ElixirSDK{Dagger: ci},
		PHP:        &PHPSDK{Dagger: ci},
		Java:       &JavaSDK{Dagger: ci},
	}
}

// Creates a dev container that has a running CLI connected to a dagger engine
func (ci *Dagger) Dev(
	ctx context.Context,
	// +optional
	target *Directory,
	// +optional
	experimentalGPUSupport bool,
) (*Container, error) {
	if target == nil {
		target = dag.Directory()
	}

	engine := ci.Engine()
	if experimentalGPUSupport {
		engine = engine.WithGPUSupport()
	}
	svc, err := engine.Service(ctx, "dev")
	if err != nil {
		return nil, err
	}
	endpoint, err := svc.Endpoint(ctx, dagger.ServiceEndpointOpts{Scheme: "tcp"})
	if err != nil {
		return nil, err
	}

	client, err := ci.CLI().File(ctx, "")
	if err != nil {
		return nil, err
	}

	return util.GoBase(ci.Source).
		WithMountedDirectory("/mnt", target).
		WithMountedFile("/usr/bin/dagger", client).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", "/usr/bin/dagger").
		WithServiceBinding("dagger-engine", svc).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", endpoint).
		WithWorkdir("/mnt"), nil
}
