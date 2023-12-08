package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"dagger.io/dagger"
)

func main() {
	os.WriteFile("foo.txt", []byte("1"), 0600)
	os.WriteFile("bar.txt", []byte("2"), 0600)
	os.WriteFile("baz.rar", []byte("3"), 0600)

	ctx := context.Background()

	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		log.Println(err)
		return
	}
	defer client.Close()

	entries, err := client.Host().Directory(".", dagger.HostDirectoryOpts{
		Include: []string{"*.rar"},
	}).Entries(ctx)
	if err != nil {
		log.Println(err)
		return
	}

	fmt.Println(entries)
}
