package main

import (
	"context"
	"path/filepath"
)

type Dirdiff struct{}

// Return an error if two directories are not identical at the given paths.
// Paths not specified in the arguments are not compared.
func (dd *Dirdiff) AssertEqual(
	ctx context.Context,
	// The first directory to compare
	a *Directory,
	// The second directory to compare
	b *Directory,
	// The paths to include in the comparison.
	paths []string,
) error {
	ctr := dag.
		Wolfi().
		Container().
		WithMountedDirectory("/mnt/a", a).
		WithMountedDirectory("/mnt/b", b).
		WithWorkdir("/mnt")
	for _, path := range paths {
		_, err := ctr.
			WithExec([]string{"diff", "-r", filepath.Join("a", path), filepath.Join("b", path)}).
			Sync(ctx)
		if err != nil {
			return err
		}
	}
	return nil
}
