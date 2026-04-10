// A module for HelloWithServices functions
package main

import (
	"context"
	"fmt"
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

// Returns the names of all services visible from the current environment.
func (m *HelloWithServices) CurrentEnvServices(ctx context.Context) ([]string, error) {
	services, err := dag.CurrentEnv().Services().List(ctx)
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

// Exercises Up.asService: for each +up service visible from the current
// environment, converts the Up to a Service via AsService and verifies the
// resulting Service yields a non-empty hostname. Returns the fully-qualified
// service names (one per successful conversion) so tests can assert on them.
func (m *HelloWithServices) CurrentEnvUpAsServiceNames(ctx context.Context) ([]string, error) {
	ups, err := dag.CurrentEnv().Services().List(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(ups))
	for _, up := range ups {
		hostname, err := up.AsService().Hostname(ctx)
		if err != nil {
			return nil, fmt.Errorf("up.asService.hostname: %w", err)
		}
		if hostname == "" {
			return nil, fmt.Errorf("up.asService yielded empty hostname")
		}
		name, err := up.Name(ctx)
		if err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

// Exercises UpGroup.asServices: materializes every Up in the current-env
// UpGroup to a Service in one call and verifies each produces a non-empty
// hostname. Returns the fully-qualified Up names (fetched in parallel via
// List) so tests can assert on them. List() and AsServices() must return
// parallel slices.
func (m *HelloWithServices) CurrentEnvGroupAsServiceNames(ctx context.Context) ([]string, error) {
	group := dag.CurrentEnv().Services()
	ups, err := group.List(ctx)
	if err != nil {
		return nil, err
	}
	services, err := group.AsServices(ctx)
	if err != nil {
		return nil, err
	}
	if len(ups) != len(services) {
		return nil, fmt.Errorf("list/asServices length mismatch: %d vs %d", len(ups), len(services))
	}
	names := make([]string, 0, len(ups))
	for i, svc := range services {
		hostname, err := svc.Hostname(ctx)
		if err != nil {
			return nil, fmt.Errorf("upGroup.asServices[%d].hostname: %w", i, err)
		}
		if hostname == "" {
			return nil, fmt.Errorf("upGroup.asServices[%d] yielded empty hostname", i)
		}
		name, err := ups[i].Name(ctx)
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
