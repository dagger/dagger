package main

import (
	"context"
	"path/filepath"
)

type SDK struct {
	Go *GoSDK
}

// type SDK interface {
// 	Lint(ctx context.Context) error
// 	Test(ctx context.Context) error
// 	Generate(ctx context.Context) (*Directory, error)
// 	Publish(ctx context.Context, tag string) error
// 	Bump(ctx context.Context, engineVersion string) error
// }

func diffDirectory(ctx context.Context, path string, original *Directory, modified *Directory) error {
	_, err := dag.Container().
		From("alpine").
		WithMountedDirectory("/mnt/original", original).
		WithMountedDirectory("/mnt/modified", modified).
		WithExec([]string{"diff", "-r", filepath.Join("/mnt/original", path), filepath.Join("/mnt/modified", path)}).
		Sync(ctx)
	return err
}

func diffDirectoryF(ctx context.Context, path string, original *Directory, modifiedF func(context.Context) (*Directory, error)) error {
	modified, err := modifiedF(ctx)
	if err != nil {
		return err
	}
	return diffDirectory(ctx, path, original, modified)
}
