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
	workdir := os.TempDir()
	folder := workdir + string(os.PathSeparator)

	for _, subdir := range []string{"foo", "bar", "baz"} {
		folder = filepath.Join(folder, subdir)
		os.Mkdir(folder, 0600)

		for _, file := range []string{".txt", ".rar", ".out"} {
			os.WriteFile(filepath.Join(folder, subdir+file), []byte(subdir), 0600)
		}
	}

	ctx := context.Background()

	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr), dagger.WithWorkdir(workdir))
	if err != nil {
		log.Println(err)
		return
	}
	defer client.Close()

	daggerdir := client.Host().Directory(".", dagger.HostDirectoryOpts{
		Include: []string{"**/*.rar", "**/*.txt"},
		Exclude: []string{"**.out"},
	})

	folder = "." + string(os.PathSeparator)
	for _, d := range []string{"foo", "bar", "baz"} {
		folder = filepath.Join(folder, d)
		entries, err := daggerdir.Entries(ctx, dagger.DirectoryEntriesOpts{Path: folder})
		if err != nil {
			log.Println(err)
			return
		}
		fmt.Printf("In %s: %v\n", folder, entries)
	}
}
