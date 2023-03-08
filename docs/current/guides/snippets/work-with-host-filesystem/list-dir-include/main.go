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
	dir := os.TempDir()
	os.WriteFile(filepath.Join(dir, "foo.txt"), []byte("1"), 0600)
	os.WriteFile(filepath.Join(dir, "bar.txt"), []byte("2"), 0600)
	os.WriteFile(filepath.Join(dir, "baz.rar"), []byte("3"), 0600)

	ctx := context.Background()

	client, err := dagger.Connect(ctx, dagger.WithWorkdir(dir))
	if err != nil {
		log.Println(err)
		return
	}
	defer client.Close()

	// highlight-start
	entries, err := client.Host().Directory(".", dagger.HostDirectoryOpts{
		Include: []string{"*.rar"},
	}).Entries(ctx)
	// highlight-end
	if err != nil {
		log.Println(err)
		return
	}

	fmt.Println(entries)
}
