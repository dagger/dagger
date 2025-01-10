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
	src := t.Dagger.Src.Directory("sdk/dotnet")
	return dag.DotnetSDKDev(dagger.DotnetSDKDevOpts{Source: src}).Test(ctx)
}

func (t DotnetSDK) TestPublish(ctx context.Context, tag string) error {
	// The SDK doesn't publish as a library at the moment.
	return nil
}

func (t DotnetSDK) Generate(ctx context.Context) (*dagger.Directory, error) {
	// The SDK is generate code at compile-time. There is no output yet.
	return dag.Directory(), nil
}

func (t DotnetSDK) Bump(ctx context.Context, version string) (*dagger.Directory, error) {
	// The SDK has no engine to bump at the moment. So skip it.
	return dag.Directory(), nil
}
