package main

import (
	"context"

	"dagger.io/dagger"
)

type Test struct{}

func (Test) Test(ctx context.Context) (string, error) {
	return "hey", nil
}

func main() {
	dagger.Serve(Test{})
}
