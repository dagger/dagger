package main

import (
	"context"

	"github.com/dagger/dagger/dev/internal/dagger"
	"golang.org/x/sync/errgroup"
)

type AllSDK struct {
	SDK *SDK // +private
}

var _ sdkBase = AllSDK{}

func (t AllSDK) Lint(ctx context.Context) error {
	_, err := forEachSDK(ctx, t.SDK.allSDKs(), func(ctx context.Context, sdk sdkBase) (struct{}, error) {
		return struct{}{}, sdk.Lint(ctx)
	})
	return err
}

func (t AllSDK) Test(ctx context.Context) error {
	_, err := forEachSDK(ctx, t.SDK.allSDKs(), func(ctx context.Context, sdk sdkBase) (struct{}, error) {
		return struct{}{}, sdk.Test(ctx)
	})
	return err
}

func (t AllSDK) Generate(ctx context.Context) (*dagger.Directory, error) {
	return t.mergeDirsForEachSDK(ctx, func(ctx context.Context, sdk sdkBase) (*dagger.Directory, error) {
		return sdk.Generate(ctx)
	})
}

func (t AllSDK) Bump(ctx context.Context, version string) (*dagger.Directory, error) {
	return t.mergeDirsForEachSDK(ctx, func(ctx context.Context, sdk sdkBase) (*dagger.Directory, error) {
		return sdk.Bump(ctx, version)
	})
}

func (t AllSDK) GenerateChangelogs(ctx context.Context, version string, bumpEnginePR string) (*dagger.Directory, error) {
	return t.mergeDirsForEachSDK(ctx, func(ctx context.Context, sdk sdkBase) (*dagger.Directory, error) {
		return sdk.GenerateChangelogs(ctx, version, bumpEnginePR)
	})
}

func (t AllSDK) mergeDirsForEachSDK(
	ctx context.Context,
	f func(context.Context, sdkBase) (*dagger.Directory, error),
) (*dagger.Directory, error) {
	dirs, err := forEachSDK(ctx, t.SDK.allSDKs(), func(ctx context.Context, sdk sdkBase) (*dagger.Directory, error) {
		dir, err := f(ctx, sdk)
		if err != nil {
			return nil, err
		}
		return dir.Sync(ctx)
	})
	if err != nil {
		return nil, err
	}

	dir := dag.Directory()
	for _, dir2 := range dirs {
		dir = dir.WithDirectory("", dir2)
	}
	return dir, nil
}

func forEachSDK[R any](
	ctx context.Context,
	sdks []sdkBase,
	f func(context.Context, sdkBase) (R, error),
) ([]R, error) {
	eg, ctx := errgroup.WithContext(ctx)
	values := make([]R, len(sdks))
	for i, sdk := range sdks {
		i, sdk := i, sdk
		eg.Go(func() error {
			v, err := f(ctx, sdk)
			if err != nil {
				return err
			}
			values[i] = v
			return nil
		})
	}
	err := eg.Wait()
	if err != nil {
		return nil, err
	}
	return values, nil
}
