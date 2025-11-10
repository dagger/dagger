package main

import (
	"context"
	"dagger/all-sdks/internal/dagger"
	"sort"

	"github.com/dagger/dagger/util/parallel"
)

type AllSdks struct{}

// List available SDKs
func (sdks *AllSdks) List() []string {
	all := all[any]()
	names := make([]string, len(all))
	for i := range all {
		names[i] = all[i].Name
	}
	sort.Strings(names)
	return names
}

// Generate all SDKs, and return the combined diff
func (sdks *AllSdks) Generate(ctx context.Context) (*dagger.Changeset, error) {
	jobs := parallel.New()
	// 2. SDK toolchains in standalone modules have a lazy signature
	type generator interface {
		Generate() *dagger.Changeset
	}
	generators := all[generator]()
	genSDKs := make([]*dagger.Changeset, len(generators))
	for i, sdk := range generators {
		jobs = jobs.WithJob(sdk.Name, func(ctx context.Context) error {
			var err error
			genSDKs[i], err = sdk.Value.Generate().Sync(ctx)
			return err
		})
	}
	if err := jobs.Run(ctx); err != nil {
		return nil, err
	}
	return changesetMerge(genSDKs...), nil
}

// Atomically bump all SDKs to the specified version
func (sdks *AllSdks) Bump(ctx context.Context, version string) (*dagger.Changeset, error) {
	type bumper interface {
		Bump(string) *dagger.Changeset
	}
	bumpers := all[bumper]()
	bumpSDKs := make([]*dagger.Changeset, len(bumpers))
	jobs := parallel.New()
	for i, sdk := range bumpers {
		jobs = jobs.WithJob(sdk.Name, func(ctx context.Context) error {
			var err error
			bumpSDKs[i], err = sdk.Value.Bump(version).Sync(ctx)
			return err
		})
	}
	if err := jobs.Run(ctx); err != nil {
		return nil, err
	}
	return changesetMerge(bumpSDKs...), nil
}

type namedSDK[T any] struct {
	Name  string
	Value T
}

// Return a list of all SDKs implementing the given interface
func all[T any]() []namedSDK[T] {
	var result []namedSDK[T]
	for _, entry := range []struct {
		name string
		sdk  any
	}{
		{"go", dag.GoSDKDev()},
		{"php", dag.PhpSDKDev()},
		{"typescript", dag.TypescriptSDKDev()},
		{"python", dag.PythonSDKDev()},
	} {
		if casted, ok := entry.sdk.(T); ok {
			result = append(result, namedSDK[T]{
				Name:  entry.name,
				Value: casted,
			})
		}
	}
	return result
}
