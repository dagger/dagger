// A module for HelloWithServices functions
package main

import (
	"context"
	"sort"

	"dagger/hello-with-services/internal/dagger"
)

type HelloWithServices struct{}

// Returns a web server service
// +up
func (m *HelloWithServices) Web() *dagger.Service {
	return dag.Container().
		From("nginx:alpine").
		WithExposedPort(80).
		AsService()
}

// Returns a redis service
// +up
func (m *HelloWithServices) Redis() *dagger.Service {
	return dag.Container().
		From("redis:alpine").
		WithExposedPort(6379).
		AsService()
}

// Returns the names of all services visible from the current workspace.
func (m *HelloWithServices) CurrentEnvServices(ctx context.Context) ([]string, error) {
	services, err := dag.CurrentWorkspace().Services().List(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(services))
	for _, svc := range services {
		name, err := svc.Name(ctx)
		if err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

func (m *HelloWithServices) Infra() *Infra {
	return &Infra{}
}

type Infra struct{}

// +up
func (i *Infra) Database() *dagger.Service {
	return dag.Container().
		From("postgres:alpine").
		WithEnvVariable("POSTGRES_PASSWORD", "test").
		WithExposedPort(5432).
		AsService()
}
