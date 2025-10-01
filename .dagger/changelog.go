package main

import (
	"github.com/dagger/dagger/.dagger/internal/dagger"
)

// Generate the changelog with 'changie merge'. Only run this manually, at release time.
func (dev *DaggerDev) GenerateChangelog() *dagger.Changeset {
	// FIXME: use pre-call filtering
	src := dev.Source.Filter(dagger.DirectoryFilterOpts{
		Include: []string{
			"**/.changes/",
			"CHANGELOG.md",
			"**/.changie.yaml",
		},
	})
	changieVersion := "1.21.0"
	return dag.Container().
		From("ghcr.io/miniscruff/changie:v"+changieVersion).
		WithWorkdir("/src").
		WithMountedDirectory(".", src).
		WithExec([]string{"/changie", "merge"}).
		Directory(".").
		Changes(src)
}
