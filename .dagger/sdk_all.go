package main

import (
	"context"

	"github.com/dagger/dagger/.dagger/internal/dagger"
	"golang.org/x/sync/errgroup"
)

type AllSDK struct {
	SDK *SDK // +private
}

var _ sdkBase = AllSDK{}

func (t AllSDK) Lint(ctx context.Context) error {
	eg, ctx := errgroup.WithContext(ctx)
	for _, sdk := range t.SDK.allSDKs() {
		eg.Go(func() error {
			return sdk.Lint(ctx)
		})
	}
	return eg.Wait()
}

func (t AllSDK) Test(ctx context.Context) error {
	eg, ctx := errgroup.WithContext(ctx)
	for _, sdk := range t.SDK.allSDKs() {
		eg.Go(func() error {
			return sdk.Test(ctx)
		})
	}
	return eg.Wait()
}

func (t AllSDK) TestPublish(ctx context.Context, tag string) error {
	eg, ctx := errgroup.WithContext(ctx)
	for _, sdk := range t.SDK.allSDKs() {
		eg.Go(func() error {
			return sdk.TestPublish(ctx, tag)
		})
	}
	return eg.Wait()
}

func (t AllSDK) Generate(ctx context.Context) (*dagger.Directory, error) {
	eg, ctx := errgroup.WithContext(ctx)
	dirs := make([]*dagger.Directory, len(t.SDK.allSDKs()))
	for i, sdk := range t.SDK.allSDKs() {
		eg.Go(func() error {
			dir, err := sdk.Generate(ctx)
			if err != nil {
				return err
			}
			dir, err = dir.Sync(ctx)
			if err != nil {
				return err
			}
			dirs[i] = dir
			return nil
		})
	}
	err := eg.Wait()
	if err != nil {
		return nil, err
	}

	dir := dag.Directory()
	for _, dir2 := range dirs {
		dir = dir.WithDirectory("", dir2)
	}
	return dir, nil
}

func (t AllSDK) Bump(ctx context.Context, version string) (*dagger.Directory, error) {
	eg, ctx := errgroup.WithContext(ctx)
	dirs := make([]*dagger.Directory, len(t.SDK.allSDKs()))
	for i, sdk := range t.SDK.allSDKs() {
		eg.Go(func() error {
			dir, err := sdk.Bump(ctx, version)
			if err != nil {
				return err
			}
			dir, err = dir.Sync(ctx)
			if err != nil {
				return err
			}
			dirs[i] = dir
			return nil
		})
	}
	err := eg.Wait()
	if err != nil {
		return nil, err
	}

	dir := dag.Directory()
	for _, dir2 := range dirs {
		dir = dir.WithDirectory("", dir2)
	}
	return dir, nil
}
