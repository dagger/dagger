package main

import (
	"context"

	"github.com/dagger/dagger/.dagger/internal/dagger"
)

type DotnetSDK struct {
	// +private
	Dagger *DaggerDev
}

func (t DotnetSDK) Name() string {
	return "dotnet"
}
func (t DotnetSDK) Lint(ctx context.Context) (MyCheckStatus, error) {
	return CheckCompleted, dag.DotnetSDKDev().Lint(ctx)
}

func (t DotnetSDK) Test(ctx context.Context) (MyCheckStatus, error) {
	src := t.Dagger.Source.Directory("sdk/dotnet")
	return CheckCompleted, dag.
		DotnetSDKDev(dagger.DotnetSDKDevOpts{Source: src}).
		Test(ctx, t.Dagger.introspectionJSON())
}

// Install the SDK locally so that it can be imported
// NOTE: this was initially called Generate(), but it doesn't do what our CI
// expects an SDK Generate() function to do.
//
// - Expected: generate client library to be committed and published
// - Actual: generate introspection.json which allows *using* the SDK from a local checkout
//
// Since this SDK at the moment cannot be published or installed, the standard Generate()
// function does not need to exist.
// WARNING: if you rename this to Generate(), it wil break CI because it adds a file to the
// repo (introspection.json) which is git-ignored and therefore not present in a clean checkout
// This is why the same check may not fail locally - you have the gitignored copy.
func (t DotnetSDK) Install(ctx context.Context) (*dagger.Changeset, error) {
	src := t.Dagger.Source.Directory("sdk/dotnet")

	relLayer := dag.
		DotnetSDKDev(dagger.DotnetSDKDevOpts{Source: src}).
		Generate(t.Dagger.introspectionJSON())
	absLayer := dag.Directory().WithDirectory("sdk/dotnet", relLayer)
	return absLayer.Changes(dag.Directory()).Sync(ctx)
}

func (t DotnetSDK) Bump(ctx context.Context, version string) (*dagger.Changeset, error) { //nolint:unparam
	// The SDK has no engine to bump at the moment. So skip it.
	return dag.Directory().Changes(dag.Directory()).Sync(ctx)
}
