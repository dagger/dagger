package main

import (
	"context"
	"errors"

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

// RequiredRepo insists on the contextual repo. Note the SDK marks every
// +defaultPath arg optional in the schema, so "required" is module-level
// policy: the module still receives nil when the context has no usable git
// repository and must guard for it.
func (m *Test) RequiredRepo(
	ctx context.Context,
	// +defaultPath="/"
	repo *dagger.GitRepository,
) (string, error) {
	if repo == nil {
		return "", errors.New("no usable git repository in context")
	}
	return repo.Head().Commit(ctx)
}

func (m *Test) RepoState(
	ctx context.Context,
	// +optional
	// +defaultPath="/"
	repo *dagger.GitRepository,
) (string, error) {
	if repo == nil {
		return "no repo", nil
	}
	clean, err := repo.Uncommitted().IsEmpty(ctx)
	if err != nil {
		return "", err
	}
	if clean {
		return "clean", nil
	}
	return "dirty", nil
}
