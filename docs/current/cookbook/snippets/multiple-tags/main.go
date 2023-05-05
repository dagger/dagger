package main

import (
    "context"
    "fmt"
    "os"

    "dagger.io/dagger"
)

func main() {
    // create Dagger client
    ctx := context.Background()
    client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
    if err != nil {
        panic(err)
    }
    defer client.Close()

    // load registry credentials from environment variables
    username := os.Getenv("DOCKERHUB_USERNAME")
    if username == "" {
      panic("DOCKERHUB_USERNAME env var must be set")
    }
    passwordPlaintext := os.Getenv("DOCKERHUB_PASSWORD")
    if passwordPlaintext == "" {
      panic("DOCKERHUB_PASSWORD env var must be set")
    }
    password := client.SetSecret("password", passwordPlaintext)

    // define multiple image tags
    tags := [4]string{"latest", "1.0-alpine", "1.0", "1.0.0"}

    // create and publish image with multiple tags
    ctr := client.Container().
        From("alpine").
        WithRegistryAuth("docker.io", username, password)

    for _, tag := range tags {
        addr, err := ctr.Publish(ctx, fmt.Sprintf("%s/alpine:%s", username, tag))
        if err != nil {
            panic(err)
        }
        fmt.Println("Published: ", addr)
    }
}