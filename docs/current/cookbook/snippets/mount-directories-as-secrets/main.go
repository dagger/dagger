package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"dagger.io/dagger"
)

func main() {
	// initialize Dagger client
	ctx := context.Background()
	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		panic(err)
	}
	defer client.Close()

	// GPG files needed for signing
	gpgFiles := []string{
		"/home/USER/.gnupg/pubring.kbx",
		"/home/USER/.gnupg/trustdb.gpg",
		"/home/USER/.gnupg/public.key",
	}

	// Container setup
	container := client.Container().From("alpine:3.17")

	// Mount GPG files
	for _, filePath := range gpgFiles {
		secret := client.Host().SetSecretFile(filepath.Base(filePath), filePath)
		container = container.WithMountedSecret("/root/.gnupg/"+filepath.Base(filePath), secret)
	}

	// Mount private keys directory
	// /home/USER/.gnupg/private-keys-v1.d/ contains a private.key file in our example
	sourceDir := "/home/USER/.gnupg/private-keys-v1.d/"
	targetDir := "/root/.gnupg/private-keys-v1.d/"
	container = container.With(mountDirectoryAsSecret(client, sourceDir, targetDir))

	// Sign the binary, and output its signature
	binaryName := "binary-name"
	out, err := container.
		WithExec([]string{"apk", "add", "--no-cache", "gnupg"}).
		WithExec([]string{"chmod", "700", "/root/.gnupg"}).
		WithExec([]string{"chmod", "700", "/root/.gnupg/private-keys-v1.d"}).
		WithExec([]string{"gpg", "--import", "/root/.gnupg/public.key"}).
		WithExec([]string{"gpg", "--import", "/root/.gnupg/private-keys-v1.d/private.key"}).
		WithWorkdir("/root").
		WithExec([]string{"gpg", "--detach-sign", "--armor", binaryName}).
		WithExec([]string{"ls", "-l", binaryName + ".asc"}).
		Stdout(ctx)

	if err != nil {
		panic(err)
	}

	fmt.Println(out)
}

func mountDirectoryAsSecret(client *dagger.Client, sourceDir, targetDir string) func(*dagger.Container) *dagger.Container {
	return func(c *dagger.Container) *dagger.Container {
		err := filepath.Walk(sourceDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			if !info.IsDir() {
				relativePath, _ := filepath.Rel(sourceDir, path)
				targetPath := filepath.Join(targetDir, relativePath)

				// Set secret to file content
				secret := client.Host().SetSecretFile(filepath.Base(path), path)

				// Mount secret as file in container
				c = c.WithMountedSecret(targetPath, secret)
			}

			return nil
		})

		if err != nil {
			panic(err)
		}

		return c
	}
}
