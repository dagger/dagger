package main

import (
	"dagger/changelog/internal/dagger"
	"fmt"
	"strings"
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

// Lookup the change notes file for the given component and version
func (c *Changelog) LookupEntry(
	// The component to look up change notes for
	// Example: "sdk/php"
	component,
	// The version to look up change notes for
	version string,
) *dagger.File {
	path := fmt.Sprintf(".changes/%s.md", version)
	if component != "" {
		path = strings.TrimSuffix(component, "/") + "/" + path
	}
	return c.Source.File(path)
}
