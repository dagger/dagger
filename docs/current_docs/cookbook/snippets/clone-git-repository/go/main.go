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

func (m *MyModule) Clone(ctx context.Context, repository string, locator Locator, id string) *dagger.Container {
	r := dag.Git(repository)
	var dir *dagger.Directory

	switch locator {
	case Branch:
		dir = r.Branch(id).Tree()
	case Tag:
		dir = r.Tag(id).Tree()
	case Commit:
		dir = r.Commit(id).Tree()
	}

	return dag.Container().
		From("alpine:latest").
		WithDirectory("/src", dir).
		WithWorkdir("/src")
}
