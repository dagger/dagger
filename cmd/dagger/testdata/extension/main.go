package main

import (
	"dagger.io/dagger"
)

type Test struct{}

func (Test) Test(ctx dagger.Context) (string, error) {
	return "hey", nil
}

func main() {
	dagger.Serve(Test{})
}
