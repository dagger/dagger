package main

import "dagger.io/dagger"

func main() {
	dagger.Serve(
		Lint{},
	)
}

type Lint struct{}
