package main

import (
	"dagger/test/internal/dagger"
	"strconv"
)

func New(
	// +optional
	// +default=23100
	port int,
) *Test {
	return &Test{
		Ctr: dag.Container().
			From("python").
			WithMountedDirectory(
				"/srv/www",
				dag.Directory().WithNewFile("index.html", "hey there"),
			).
			WithWorkdir("/srv/www").
			WithExposedPort(port).
			WithDefaultArgs([]string{"python", "-m", "http.server", strconv.Itoa(port)}),
	}
}

type Test struct {
	Ctr *dagger.Container
}
