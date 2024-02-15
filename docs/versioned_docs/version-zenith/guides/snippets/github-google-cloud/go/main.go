package main

import (
	"context"
	"fmt"
)

type MyModule struct {
	Source *Directory
}

// constructor
func New(source *Directory) *MyModule {
	return &MyModule{
		Source: source,
	}
}

// build a container
func (m *MyModule) Build() *Container {
	return dag.Container().
		From("node:21").
		WithDirectory("/home/node", m.Source).
		WithWorkdir("/home/node").
		WithExec([]string{"npm", "install"}).
		WithEntrypoint([]string{"npm", "start"})
}

// publish an image
// example: dagger call --source . publish --project PROJECT --location LOCATION --repository REPOSITORY/APPNAME --credential env:GOOGLE_JSON
func (m *MyModule) Publish(ctx context.Context, project string, location string, repository string, credential *Secret) (string, error) {
	registry := fmt.Sprintf("%s-docker.pkg.dev/%s/%s", location, project, repository)
	return m.Build().
		WithRegistryAuth(fmt.Sprintf("%s-docker.pkg.dev", location), "_json_key", credential).
		Publish(ctx, registry)
}

// deploy an image to Google Cloud Run
// example: dagger call --source . deploy --project PROJECT --registry-location LOCATION --repository REPOSITORY/APPNAME --service-location LOCATION --service SERVICE  --credential env:GOOGLE_JSON
func (m *MyModule) Deploy(ctx context.Context, project string, registryLocation string, repository string, serviceLocation string, service string, credential *Secret) (string, error) {

	// publish image
	addr, err := m.Publish(ctx, project, registryLocation, repository, credential)
	if err != nil {
		panic(err)
	}

	// update service with new image
	return dag.GoogleCloudRun().UpdateService(ctx, project, serviceLocation, service, addr, 3000, credential)
}
