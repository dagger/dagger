// A module with both +check and +generate functions to test generate-as-check
package main

import (
	"context"

	"dagger/hello-with-generate-checks/internal/dagger"
)

type HelloWithGenerateChecks struct{}

// A regular passing check
// +check
func (m *HelloWithGenerateChecks) PassingCheck(ctx context.Context) error {
	_, err := dag.Container().From("alpine:3").WithExec([]string{"sh", "-c", "exit 0"}).Sync(ctx)
	return err
}

// A generator that produces an empty changeset (should pass as check)
// +generate
func (m *HelloWithGenerateChecks) EmptyGenerate() *dagger.Changeset {
	return dag.Directory().Changes(dag.Directory())
}

// A generator that produces changes (should fail as check)
// +generate
func (m *HelloWithGenerateChecks) NonEmptyGenerate() *dagger.Changeset {
	return dag.Directory().
		WithNewFile("generated.txt", "this file was generated").
		Changes(dag.Directory())
}
