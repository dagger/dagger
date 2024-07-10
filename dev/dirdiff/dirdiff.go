package main

import (
	"context"
	"path/filepath"

	"github.com/dagger/dagger/dev/dirdiff/internal/dagger"
)

type Dirdiff struct{}

// Return an error if two directories are not identical at the given paths.
// Paths not specified in the arguments are not compared.
func (dd *Dirdiff) AssertEqual(
	ctx context.Context,
	// The first directory to compare
	a *dagger.Directory,
	// The second directory to compare
	b *dagger.Directory,
	// The paths to include in the comparison.
	paths []string,
) error {
	ctr := dag.
		Wolfi().
		Container(dagger.WolfiContainerOpts{Packages: []string{
			// install diffutils, since busybox diff -r sometimes doesn't output anything
			"diffutils",
		}}).
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
