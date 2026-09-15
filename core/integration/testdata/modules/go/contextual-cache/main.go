package main

import (
	"context"
	"crypto/rand"

	"dagger/test/internal/dagger"
)

type Test struct{}

func (m *Test) ContextDir(
	ctx context.Context,
	// +defaultPath="."
	dir *dagger.Directory,
) (string, error) {
	contents, err := dir.File("dagger.json").Contents(ctx)
	if err != nil {
		return "", err
	}
	return rand.Text() + "|" + contents, nil
}

func (m *Test) ContextFile(
	ctx context.Context,
	// +defaultPath="dagger.json"
	file *dagger.File,
) (string, error) {
	contents, err := file.Contents(ctx)
	if err != nil {
		return "", err
	}
	return rand.Text() + "|" + contents, nil
}

func (m *Test) ContextGitRepository(
	ctx context.Context,
	// +defaultPath="."
	repo *dagger.GitRepository,
) (string, error) {
	commit, err := repo.Head().Commit(ctx)
	if err != nil {
		return "", err
	}
	return rand.Text() + "|" + commit, nil
}

func (m *Test) ContextGitRef(
	ctx context.Context,
	// +defaultPath="."
	ref *dagger.GitRef,
) (string, error) {
	commit, err := ref.Commit(ctx)
	if err != nil {
		return "", err
	}
	return rand.Text() + "|" + commit, nil
}
