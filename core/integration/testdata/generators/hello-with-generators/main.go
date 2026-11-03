package main

import (
	"context"
	"errors"

	"dagger/hello-with-generators/internal/dagger"
)

type HelloWithGenerators struct{}

// Return a changeset with a new file
// +generate
func (m *HelloWithGenerators) GenerateFiles() *dagger.Changeset {
	return dagger.Connect().Directory().
		WithNewFile("foo", "bar").
		Changes(dagger.Connect().Directory())
}

// Return a changeset with a new file
// +generate
func (m *HelloWithGenerators) GenerateOtherFiles() *dagger.Changeset {
	return dagger.Connect().Directory().
		WithNewFile("bar", "foo").
		Changes(dagger.Connect().Directory())
}

// Return an empty changeset
// +generate
func (m *HelloWithGenerators) EmptyChangeset() *dagger.Changeset {
	return dagger.Connect().Directory().Changes(dagger.Connect().Directory())
}

// Return an error
// +generate
func (m *HelloWithGenerators) ChangesetFailure() (*dagger.Changeset, error) {
	return nil, errors.New("could not generate the changeset")
}

// Return a changeset whose evaluation fails lazily: the function succeeds, but
// the exec backing the changeset only runs (and fails) when the changeset is
// merged/forced during `dagger generate`. The stderr marker is passed via an
// env var so it does NOT appear in the command text — the test can then prove
// the message surfaces stderr specifically, not just the failed command.
// +generate
func (m *HelloWithGenerators) LazyExecFailure() *dagger.Changeset {
	failed := dagger.Connect().Container().
		From("alpine").
		WithEnvVariable("MARKER", "STDERR_ONLY_MARKER").
		WithExec([]string{"sh", "-c", "echo $MARKER >&2; exit 3"}).
		Directory("/")
	return failed.Changes(dagger.Connect().Directory())
}

func (m *HelloWithGenerators) WorkspaceGeneratorsEmpty(ctx context.Context, ws *dagger.Workspace) (bool, error) {
	items, err := ws.Artifacts(dagger.WorkspaceArtifactsOpts{Include: []string{"toolchain-generators"}}).FilterTypes([]string{"Generator"}).Items(ctx)
	if err != nil {
		return false, err
	}
	empty := true
	for _, artifact := range items {
		id, err := artifact.Value().ID(ctx)
		if err != nil {
			return false, err
		}
		itemEmpty, err := dagger.Ref[*dagger.Changeset](dagger.Connect(), id).IsEmpty(ctx)
		if err != nil {
			return false, err
		}
		empty = empty && itemEmpty
	}
	return empty, nil
}

type MetaGen struct{}

func (m *HelloWithGenerators) OtherGenerators() *MetaGen {
	return &MetaGen{}
}

// +generate
func (mg *MetaGen) GenThings() *dagger.Changeset {
	return dagger.Connect().Directory().
		WithNewFile("meta-gen", "generated").
		Changes(dagger.Connect().Directory())
}
