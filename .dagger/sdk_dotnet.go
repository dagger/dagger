package main

import (
	"context"

	"github.com/dagger/dagger/.dagger/internal/dagger"
)

type DotnetSDK struct {
	// +private
	Dagger *DaggerDev
}

func (t DotnetSDK) Lint(ctx context.Context) error {
	return dag.DotnetSDKDev().Lint(ctx)
}

func (t DotnetSDK) Test(ctx context.Context) error {
	installer := t.Dagger.installer("sdk")
	introspection := t.Dagger.introspection(installer)
	src := t.Dagger.Source.Directory("sdk/dotnet")
	return dag.DotnetSDKDev(dagger.DotnetSDKDevOpts{Source: src}).Test(ctx, introspection)
}

func (t DotnetSDK) TestPublish(ctx context.Context, tag string) error {
	// The SDK doesn't publish as a library at the moment.
	return nil
}

func (t DotnetSDK) Generate(ctx context.Context) (*dagger.Directory, error) {
	installer := t.Dagger.installer("sdk")
	introspection := t.Dagger.introspection(installer)
	src := t.Dagger.Source.Directory("sdk/dotnet")

	return dag.
		Directory().
		WithDirectory(
			"sdk/dotnet",
			dag.DotnetSDKDev(dagger.DotnetSDKDevOpts{Source: src}).Generate(introspection),
		), nil
}

func (t DotnetSDK) Bump(ctx context.Context, version string) (*dagger.Directory, error) {
	// The SDK has no engine to bump at the moment. So skip it.
	return dag.Directory(), nil
}
