package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"dagger.io/dagger"
)

func main() {
	ctx := context.Background()

	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		log.Println(err)
		return
	}
	defer client.Close()

	contents, err := client.Container().
		From("alpine:latest").
		WithDirectory("/host", client.Host().Directory("/tmp/sandbox")).
		WithExec([]string{"/bin/sh", "-c", `echo foo > /host/bar`}).
		Directory("/host").
		Export(ctx, "/tmp/sandbox")
	if err != nil {
		log.Println(err)
		return
	}

	fmt.Println(contents)
}
