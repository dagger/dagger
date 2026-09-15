package main

import "dagger/test/internal/dagger"

type Test struct{}

func (m *Test) FnRepo(repo *dagger.GitRepository) *dagger.Directory {
	return repo.Head().Tree()
}

func (m *Test) FnRef(ref *dagger.GitRef) *dagger.Directory {
	return ref.Tree()
}
