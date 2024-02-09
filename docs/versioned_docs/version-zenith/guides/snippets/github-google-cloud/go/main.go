package main

import (
	"context"
	"strings"

	run "cloud.google.com/go/run/apiv2"
	runpb "cloud.google.com/go/run/apiv2/runpb"
	"dagger.io/dagger/dag"
	"google.golang.org/api/option"
)

type MyModule struct{}

// build a container
func (m *MyModule) Build(source *Directory) *Container {
	return dag.Container().
		From("node:21").
		WithDirectory("/src", source).
		WithWorkdir("/src").
		WithExec([]string{"cp", "-R", ".", "/home/node"}).
		WithWorkdir("/home/node").
		WithExec([]string{"npm", "install"}).
		WithEntrypoint([]string{"npm", "start"})
}

// publish an image
// example: dagger call publish --source . --registry REGISTRY/myapp --credential env:GOOGLE_JSON
func (m *MyModule) Publish(ctx context.Context, source *Directory, registry string, credential *Secret) (string, error) {
	split := strings.Split(registry, "/")
	return m.Build(source).
		WithRegistryAuth(split[0], "_json_key", credential).
		Publish(ctx, registry)
}

// deploy an image to Google Cloud Run
// example: dagger call publish --source . --registry REGISTRY/myapp --service SERVICE --credential env:GOOGLE_JSON
func (m *MyModule) Deploy(ctx context.Context, source *Directory, service string, registry string, credential *Secret) (string, error) {

	// get JSON secret
	json, err := credential.Plaintext(ctx)
	b := []byte(json)
	gcrClient, err := run.NewServicesClient(ctx, option.WithCredentialsJSON(b))
	if err != nil {
		panic(err)
	}
	defer gcrClient.Close()

	// publish image
	addr, err := m.Publish(ctx, source, registry, credential)
	if err != nil {
		panic(err)
	}

	// define service request
	gcrRequest := &runpb.UpdateServiceRequest{
		Service: &runpb.Service{
			Name: service,
			Template: &runpb.RevisionTemplate{
				Containers: []*runpb.Container{
					{
						Image: addr,
						Ports: []*runpb.ContainerPort{
							{
								Name:          "http1",
								ContainerPort: 1323,
							},
						},
					},
				},
			},
		},
	}

	// update service
	gcrOperation, err := gcrClient.UpdateService(ctx, gcrRequest)
	if err != nil {
		panic(err)
	}

	// wait for service request completion
	gcrResponse, err := gcrOperation.Wait(ctx)
	if err != nil {
		panic(err)
	}

	// return service URL
	return gcrResponse.Uri, err
}
