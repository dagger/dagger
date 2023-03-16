package main

import (
	"context"
	"fmt"
	"os"

	run "cloud.google.com/go/run/apiv2"
	runpb "cloud.google.com/go/run/apiv2/runpb"
	"dagger.io/dagger"
)

const GCR_SERVICE_URL = "projects/PROJECT/locations/us-central1/services/myapp"
const GCR_PUBLISH_ADDRESS = "gcr.io/PROJECT/myapp"

func main() {
	// create Dagger client
	ctx := context.Background()
	daggerClient, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stdout))
	if err != nil {
		panic(err)
	}
	defer daggerClient.Close()

	// get working directory on host
	source := daggerClient.Host().Directory(".", dagger.HostDirectoryOpts{
		Exclude: []string{"ci", "node_modules"},
	})

	// build application
	node := daggerClient.Container(dagger.ContainerOpts{Platform: "linux/amd64"}).
		From("node:16")

	c := node.
		WithMountedDirectory("/src", source).
		WithWorkdir("/src").
		WithExec([]string{"cp", "-R", ".", "/home/node"}).
		WithWorkdir("/home/node").
		WithExec([]string{"npm", "install"}).
		WithEntrypoint([]string{"npm", "start"})

	// publish container to Google Container Registry
	addr, err := c.Publish(ctx, GCR_PUBLISH_ADDRESS)
	if err != nil {
		panic(err)
	}

	// print ref
	fmt.Println("Published at:", addr)

	// create Google Cloud Run client
	gcrClient, err := run.NewServicesClient(ctx)
	if err != nil {
		panic(err)
	}
	defer gcrClient.Close()

	// define service request
	gcrRequest := &runpb.UpdateServiceRequest{
		Service: &runpb.Service{
			Name: GCR_SERVICE_URL,
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

	// print ref
	fmt.Println("Deployment for image", addr, "now available at", gcrResponse.Uri)

}
