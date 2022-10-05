package main

import (
	"os"

	"go.dagger.io/dagger/sdk/go/dagger"
)

type Test struct{}

func (Test) TestMount(ctx dagger.Context, in dagger.FSID) (string, error) {
	bytes, err := os.ReadFile("/mnt/in/foo")
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}

func main() {
	dagger.Serve(Test{})
}
