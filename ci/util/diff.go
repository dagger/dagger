package util

import (
	"context"
	"path/filepath"

	"github.com/dagger/dagger/ci/internal/dagger"
)

func DiffDirectory(ctx context.Context, path string, original *dagger.Directory, modified *dagger.Directory) error {
	_, err := dag.Container().
		From("alpine").
		WithMountedDirectory("/mnt/original", original).
		WithMountedDirectory("/mnt/modified", modified).
		WithWorkdir("/mnt").
		WithExec([]string{"diff", "-r", filepath.Join("original", path), filepath.Join("modified", path)}).
		Sync(ctx)
	return err
}

func DiffDirectoryF(ctx context.Context, path string, original *dagger.Directory, modifiedF func(context.Context) (*dagger.Directory, error)) error {
	modified, err := modifiedF(ctx)
	if err != nil {
		return err
	}
	return DiffDirectory(ctx, path, original, modified)
}
