package main

import (
  "context"
)

type MyModule struct{}

func (m *MyModule) SimpleDirectory(ctx context.Context) (string, error) {
	return dag.
		Git("https://github.com/dagger/dagger.git").
		Head().
		Tree().
		Terminal().
		File("README.md").
		Contents(ctx)
}
