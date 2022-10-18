package main

import (
	"os"

	"github.com/dagger/dagger/sdk/go/dagger"
	"github.com/dagger/dagger/sdk/go/dagger/api"
)

type Test struct{}

func (Test) TestMount(ctx dagger.Context, in api.DirectoryID) (string, error) {
	bytes, err := os.ReadFile("/mnt/in/foo")
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}

func main() {
	dagger.Serve(Test{})
}
