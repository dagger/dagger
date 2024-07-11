package main

import (
	"context"
	"fmt"

	"main/internal/dagger"
)

type MyModule struct{}

// build an image
func (m *MyModule) Build(source *dagger.Directory) *dagger.Container {
	return dag.Container().
		From("node:21").
		WithDirectory("/home/node", source).
		WithWorkdir("/home/node").
		WithExec([]string{"npm", "install"}).
		WithEntrypoint([]string{"npm", "start"})
}

// publish an image
// example: dagger call publish --source . --project PROJECT --location LOCATION --repository REPOSITORY/APPNAME --credential env:GOOGLE_JSON
func (m *MyModule) Publish(ctx context.Context, source *dagger.Directory, project string, location string, repository string, credential *dagger.Secret) (string, error) {
	registry := fmt.Sprintf("%s-docker.pkg.dev/%s/%s", location, project, repository)
	return m.Build(source).
		WithRegistryAuth(fmt.Sprintf("%s-docker.pkg.dev", location), "_json_key", credential).
		Publish(ctx, registry)
}

// deploy an image to Google Cloud Run
// example: dagger call deploy --source . --project PROJECT --registry-location LOCATION --repository REPOSITORY/APPNAME --service-location LOCATION --service SERVICE  --credential env:GOOGLE_JSON
func (m *MyModule) Deploy(ctx context.Context, source *dagger.Directory, project, registryLocation, repository, serviceLocation, service string, credential *dagger.Secret) (string, error) {
	// publish image
	addr, err := m.Publish(ctx, source, project, registryLocation, repository, credential)
	if err != nil {
		return "", err
	}

	// update service with new image
	return dag.GoogleCloudRun().UpdateService(ctx, project, serviceLocation, service, addr, 3000, credential)
}
