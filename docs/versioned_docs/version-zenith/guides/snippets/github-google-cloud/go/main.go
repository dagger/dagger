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

// create a build
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
// export GC_TOKEN_NOT_BASE64=`cat /home/vikram/public/credentials/vikram-experiments-69a5e0f9f523.json`
// dagger call publish --registry us-central1-docker.pkg.dev/vikram-experiments/vikram-test/myapp --credential env:GC_TOKEN_NOT_BASE64
func (m *MyModule) Publish(ctx context.Context, source *Directory, registry string, credential *Secret) (string, error) {
	split := strings.Split(registry, "/")
	return m.Build(source).
		WithRegistryAuth(split[0], "_json_key", credential).
		Publish(ctx, registry)
}

// dagger call deploy --registry us-central1-docker.pkg.dev/vikram-experiments/vikram-test/myapp --credential env:GC_TOKEN_NOT_BASE64 --service projects/vikram-experiments/locations/us-central1/services/myapp
func (m *MyModule) Deploy(ctx context.Context, source *Directory, service string, registry string, credential *Secret) (string, error) {

	json, err := credential.Plaintext(ctx)
	b := []byte(json)
	gcrClient, err := run.NewServicesClient(ctx, option.WithCredentialsJSON(b))
	if err != nil {
		panic(err)
	}
	defer gcrClient.Close()

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
	return gcrResponse.Uri, err
}
