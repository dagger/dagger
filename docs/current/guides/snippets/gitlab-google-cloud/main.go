package main

import (
    "context"
    "fmt"
    "os"

    "dagger.io/dagger"
    run "cloud.google.com/go/run/apiv2"
    runpb "cloud.google.com/go/run/apiv2/runpb"

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
      Exclude: []string{"ci"},
    })

    // build application
    builder := daggerClient.Container(dagger.ContainerOpts{Platform: "linux/amd64"}).
        From("golang:1.19").
        WithMountedDirectory("/src", source).
        WithWorkdir("/src").
        WithEnvVariable("CGO_ENABLED", "0").
        WithExec([]string{"go", "build", "-o", "myapp"})

    // add binary to alpine base
    prodImage := daggerClient.Container(dagger.ContainerOpts{Platform: "linux/amd64"}).
        From("alpine").
        WithFile("/bin/myapp", builder.File("/src/myapp")).
        WithEntrypoint([]string{"/bin/myapp"})

    // publish container to Google Container Registry
    addr, err := prodImage.Publish(ctx, GCR_PUBLISH_ADDRESS)
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
