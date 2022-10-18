package main

import (
	"os"

	"dagger.io/dagger"
	"dagger.io/dagger/api"
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
