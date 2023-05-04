package main

import (
	"os"

	"dagger.io/dagger"
)

type Test struct{}

func (Test) TestMount(ctx dagger.Context, in dagger.DirectoryID) (string, error) {
	bytes, err := os.ReadFile("/mnt/in/foo")
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}

func main() {
	dagger.Serve(Test{})
}
