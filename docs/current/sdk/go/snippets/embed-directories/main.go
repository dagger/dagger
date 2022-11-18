package main

import (
	"context"
	"embed"
	"fmt"
	"io/fs"

	"dagger.io/dagger"
)

// create a copy of an embed directory
func copyEmbedDir(e fs.FS, dir *dagger.Directory) (*dagger.Directory, error) {
	err := fs.WalkDir(e, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		content, err := fs.ReadFile(e, path)
		if err != nil {
			return err
		}

		dir = dir.WithNewFile(path, string(content))

		return nil
	})
	if err != nil {
		return nil, err
	}
	return dir, nil
}

//go:embed example
var e embed.FS

func main() {
	ctx := context.Background()

	// Init Dagger client
	client, err := dagger.Connect(ctx)
	if err != nil {
		panic(err)
	}
	defer client.Close()

	// Copy embed files to dir, a newly created directory.
	dir := client.Directory()
	dir, err = copyEmbedDir(e, dir)
	if err != nil {
		panic(err)
	}

	// Mount above directory ID and
	container := client.Container().From("alpine:3.16.2").WithMountedDirectory("/embed", dir)

	// List files
	out, err := container.Exec(dagger.ContainerExecOpts{
		Args: []string{"ls", "-lR", "/embed/"},
	}).Stdout(ctx)
	if err != nil {
		panic(err)
	}

	fmt.Printf("%s", out)
}
