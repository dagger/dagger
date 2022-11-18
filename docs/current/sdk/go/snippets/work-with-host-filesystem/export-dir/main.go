package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"dagger.io/dagger"
)

func main() {
	hostdir := os.TempDir()

	ctx := context.Background()

	client, err := dagger.Connect(ctx)
	if err != nil {
		log.Println(err)
		return
	}
	defer client.Close()

	// highlight-start
	_, err = client.Container().From("alpine:latest").
		WithWorkdir("/tmp").
		WithExec([]string{"wget", "https://dagger.io"}).
		Directory(".").
		Export(ctx, hostdir)
	if err != nil {
		log.Println(err)
		return
	}
	// highlight-end
	contents, err := os.ReadFile(filepath.Join(hostdir, "index.html"))
	if err != nil {
		log.Println(err)
		return
	}
	fmt.Println(string(contents))
}
