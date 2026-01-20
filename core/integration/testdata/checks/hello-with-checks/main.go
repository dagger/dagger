// A module for HelloWithChecks functions
package main

import (
	"context"
	"sort"

	"dagger/hello-with-checks/internal/dagger"
)

type HelloWithChecks struct {
	BaseImage string
}

func New(
	//+default="alpine:3"
	baseImage string,
) *HelloWithChecks {
	return &HelloWithChecks{
		BaseImage: baseImage,
	}
}

// Returns a passing check
// +check
func (m *HelloWithChecks) PassingCheck(ctx context.Context) error {
	_, err := dag.Container().From(m.BaseImage).WithExec([]string{"sh", "-c", "exit 0"}).Sync(ctx)
	return err
}

// Returns a failing check
// +check
func (m *HelloWithChecks) FailingCheck(ctx context.Context) error {
	_, err := dag.Container().From(m.BaseImage).WithExec([]string{"sh", "-c", "exit 1"}).Sync(ctx)
	return err
}

// Returns a container which runs as a passing check
// +check
func (m *HelloWithChecks) PassingContainer() *dagger.Container {
	return dag.Container().From(m.BaseImage).WithExec([]string{"sh", "-c", "exit 0"})
}

// Returns a container which runs as a failing check
// +check
func (m *HelloWithChecks) FailingContainer() *dagger.Container {
	return dag.Container().From(m.BaseImage).WithExec([]string{"sh", "-c", "exit 1"})
}

// Returns the names of all checks visible from the current environment.
func (m *HelloWithChecks) CurrentEnvChecks(ctx context.Context) ([]string, error) {
	checks, err := dag.CurrentEnv().Checks().List(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(checks))
	for _, check := range checks {
		name, err := check.Name(ctx)
		if err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}
