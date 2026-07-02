package main

import (
	"context"
	"dagger/test/internal/dagger"
)

type Test struct{}

func (m *Test) OptionalRepo(
	ctx context.Context,
	// +optional
	// +defaultPath="/"
	repo *dagger.GitRepository,
) (string, error) {
	if repo == nil {
		return "no repo", nil
	}
	commit, err := repo.Head().Commit(ctx)
	if err != nil {
		return "", err
	}
	return "repo@" + commit, nil
}

func (m *Test) OptionalRef(
	ctx context.Context,
	// +optional
	// +defaultPath="/"
	ref *dagger.GitRef,
) (string, error) {
	if ref == nil {
		return "no ref", nil
	}
	commit, err := ref.Commit(ctx)
	if err != nil {
		return "", err
	}
	return "ref@" + commit, nil
}

func (m *Test) RequiredRepo(
	ctx context.Context,
	// +defaultPath="/"
	repo *dagger.GitRepository,
) (string, error) {
	return repo.Head().Commit(ctx)
}
