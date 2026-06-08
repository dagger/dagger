package main

import (
	"context"
	"dagger/my-module/internal/dagger"
)

type MyModule struct{}

type Locator string

const (
	Branch Locator = "BRANCH"
	Tag    Locator = "TAG"
	Commit Locator = "COMMIT"
)

func (m *MyModule) Clone(ctx context.Context, repository string, locator Locator, ref string) *dagger.Container {
	r := dag.Git(repository)
	var d *dagger.Directory

	switch locator {
	case Branch:
		d = r.Branch(ref).Tree()
	case Tag:
		d = r.Tag(ref).Tree()
	case Commit:
		d = r.Commit(ref).Tree()
	}

	return dag.Container().
		From("alpine:latest").
		WithDirectory("/src", d).
		WithWorkdir("/src")
}
