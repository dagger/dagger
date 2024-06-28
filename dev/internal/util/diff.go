package util

import (
	"context"
	"path/filepath"

	"github.com/dagger/dagger/dev/internal/dagger"
)

func DiffDirectory(ctx context.Context, original *dagger.Directory, modified *dagger.Directory, paths ...string) error {
	ctr := dag.Container().
		From("alpine").
		WithMountedDirectory("/mnt/original", original).
		WithMountedDirectory("/mnt/modified", modified).
		WithWorkdir("/mnt")
	for _, path := range paths {
		_, err := ctr.
			WithExec([]string{"diff", "-r", filepath.Join("original", path), filepath.Join("modified", path)}).
			Sync(ctx)
		if err != nil {
			return err
		}
	}
	return nil
}

func DiffDirectoryF(ctx context.Context, original *dagger.Directory, modifiedF func(context.Context) (*dagger.Directory, error), paths ...string) error {
	modified, err := modifiedF(ctx)
	if err != nil {
		return err
	}
	return DiffDirectory(ctx, original, modified, paths...)
}
