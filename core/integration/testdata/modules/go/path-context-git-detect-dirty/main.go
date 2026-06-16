package main

import (
	"context"

	"dagger/test/internal/dagger"
)

type Test struct{}

func (m *Test) IsDirty(
	ctx context.Context,
	// +defaultPath="./.git"
	git *dagger.GitRepository,
) (bool, error) {
	clean, err := git.Uncommitted().IsEmpty(ctx)
	return !clean, err
}
