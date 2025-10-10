package main

import (
	"dagger/changelog/internal/dagger"
)

func New(
	// +defaultPath="/"
	// +ignore=[
	//  "**",
	//  "!**/.changes/",
	//  "!CHANGELOG.md",
	//  "!**/.changie.yaml"
	// ]
	source *dagger.Directory,
) *Changelog {
	return &Changelog{Source: source}
}

type Changelog struct {
	Source *dagger.Directory // +private
}

// Generate the changelog with 'changie merge'. Only run this manually, at release time.
func (c *Changelog) Generate() *dagger.Changeset {
	changieVersion := "1.21.0"
	return dag.Container().
		From("ghcr.io/miniscruff/changie:v"+changieVersion).
		WithWorkdir("/src").
		WithMountedDirectory(".", c.Source).
		WithExec([]string{"/changie", "merge"}).
		Directory(".").
		Changes(c.Source)
}
