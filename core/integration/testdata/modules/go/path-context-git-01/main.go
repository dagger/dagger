package main

import (
	"context"
	"dagger/test/internal/dagger"
)

type Test struct{}

func (m *Test) TestRepoLocal(
	ctx context.Context,
	// +defaultPath="./.git"
	git *dagger.GitRepository,
) (string, error) {
	return m.commitAndRef(ctx, git.Head())
}

func (m *Test) TestRepoLocalAbs(
	ctx context.Context,
	// +defaultPath="/"
	git *dagger.GitRepository,
) (string, error) {
	return m.commitAndRef(ctx, git.Head())
}

func (m *Test) TestRepoRemote(
	ctx context.Context,
	// +defaultPath="https://github.com/dagger/dagger.git"
	git *dagger.GitRepository,
) (string, error) {
	return m.commitAndRef(ctx, git.Tag("v0.18.2"))
}

func (m *Test) TestRefLocal(
	ctx context.Context,
	// +defaultPath="./.git"
	git *dagger.GitRef,
) (string, error) {
	return m.commitAndRef(ctx, git)
}

func (m *Test) TestRefRemote(
	ctx context.Context,
	// +defaultPath="https://github.com/dagger/dagger.git#v0.18.3"
	git *dagger.GitRef,
) (string, error) {
	return m.commitAndRef(ctx, git)
}

func (m *Test) commitAndRef(ctx context.Context, ref *dagger.GitRef) (string, error) {
	commit, err := ref.Commit(ctx)
	if err != nil {
		return "", err
	}
	reference, err := ref.Ref(ctx)
	if err != nil {
		return "", err
	}
	return reference + "@" + commit, nil
}
