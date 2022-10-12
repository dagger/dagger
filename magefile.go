//go:build mage
// +build mage

package main

import (
	"context"
	"errors"

	"github.com/magefile/mage/mg" // mg contains helpful utility functions, like Deps
	"go.dagger.io/dagger/codegen/generator"
	"go.dagger.io/dagger/engine"
	"go.dagger.io/dagger/sdk/go/dagger/api"
)

type Lint mg.Namespace

// Run all lint targets
func (t Lint) All(ctx context.Context) error {
	mg.Deps(t.Codegen)
	return nil
}

// Lint SDK code generation
func (Lint) Codegen(ctx context.Context) error {
	return engine.Start(ctx, nil, func(ctx engine.Context) error {
		// generate the api
		generated, err := generator.IntrospectAndGenerate(ctx, generator.Config{
			Package: "api",
		})
		if err != nil {
			return err
		}

		// grab the file from the repo
		core := api.New(ctx.Client)
		src, err := core.Host().Workdir().Read().File("sdk/go/dagger/api/api.gen.go").Contents(ctx)
		if err != nil {
			return err
		}

		// compare the two
		if string(generated) != src {
			return errors.New("generated api mismatch. please run `go generate ./...`")
		}

		return nil
	})
}
