package main

import (
	"context"
	"dagger/my-module/internal/dagger"
	"fmt"
	"os"
)

type MyModule struct{}

// Copy a file to the Dagger module runtime container for custom processing
func (m *MyModule) CopyFile(ctx context.Context, source *dagger.File) {
	source.Export(ctx, "foo.txt")
	// your custom logic here
	// for example, read and print the file in the Dagger Engine container
	fmt.Println(os.ReadFile("foo.txt"))
}
