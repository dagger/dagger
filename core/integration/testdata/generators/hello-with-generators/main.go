package main

import (
	"dagger/hello-with-generators/internal/dagger"
	"errors"
)

type HelloWithGenerators struct{}

// Return a changeset with a new file
// +generate
func (m *HelloWithGenerators) GenerateFiles() *dagger.Changeset {
	return dag.Directory().
		WithNewFile("foo", "bar").
		Changes(dag.Directory())
}

// Return a changeset with a new file
// +generate
func (m *HelloWithGenerators) GenerateOtherFiles() *dagger.Changeset {
	return dag.Directory().
		WithNewFile("bar", "foo").
		Changes(dag.Directory())
}

// Return an empty changeset
// +generate
func (m *HelloWithGenerators) EmptyChangeset() *dagger.Changeset {
	return dag.Directory().Changes(dag.Directory())
}

// Return an error
// +generate
func (m *HelloWithGenerators) ChangesetFailure() (*dagger.Changeset, error) {
	return nil, errors.New("could not generate the changeset")
}

type MetaGen struct{}

func (m *HelloWithGenerators) OtherGenerators() *MetaGen {
	return &MetaGen{}
}

// +generate
func (mg *MetaGen) GenThings() *dagger.Changeset {
	return dag.Directory().
		WithNewFile("meta-gen", "generated").
		Changes(dag.Directory())
}
