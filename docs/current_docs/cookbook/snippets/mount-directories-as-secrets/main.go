package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"dagger.io/dagger"
)

func main() {
	ctx := context.Background()
	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		panic(err)
	}
	defer client.Close()

	gpgKey := os.Getenv("GPG_KEY")
	if gpgKey == "" {
		gpgKey = "public"
	}

	// Export file signature
	ok, err := client.Container().
		From("alpine:3.17").
		WithExec([]string{"apk", "add", "--no-cache", "gnupg"}).
		With(mountedSecretDirectory(client, "/root/.gnupg", "~/.gnupg")).
		WithWorkdir("/root").
		WithMountedFile("myapp", client.Host().File("myapp")).
		WithExec([]string{"gpg", "--detach-sign", "--armor", "-u", gpgKey, "myapp"}).
		File("myapp.asc").
		Export(ctx, "myapp.asc")
	if !ok || err != nil {
		panic(err)
	}

	fmt.Println("Signature exported successfully")
}

func mountedSecretDirectory(client *dagger.Client, targetPath, sourcePath string) func(*dagger.Container) *dagger.Container {
	return func(c *dagger.Container) *dagger.Container {
		sourceDir := filepath.Join(os.Getenv("HOME"), sourcePath[2:])
		filepath.Walk(sourceDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			if info.Mode().IsRegular() {
				relativePath, _ := filepath.Rel(sourceDir, path)
				target := filepath.Join(targetPath, relativePath)
				secret := client.Host().SetSecretFile(filepath.Base(path), path)
				c = c.WithMountedSecret(target, secret)
			}

			return nil
		})

		// Fix directory permissions
		c = c.WithExec([]string{"sh", "-c", fmt.Sprintf("find %s -type d -exec chmod 700 {} \\;", targetPath)})

		return c
	}
}
