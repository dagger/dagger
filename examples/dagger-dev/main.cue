package main

// A dagger configuration to build and test the dagger source code.
// This configuration can easily be adapted to build and test any go project.
//
//
// Example:
//   dagger compute ./examples/dagger-dev --input-dir repository=/path/to/go/project

import (
	"dagger.io/dagger"
	"dagger.io/go"
	"dagger.io/docker"
)

repository: dagger.#Dir

// Build `dagger` using Go
build: go.#Build & {
	source:   repository
	packages: "./cmd/dagger"
	output:   "/usr/local/bin/dagger"
}

// Run go tests
test: go.#Test & {
	source:   repository
	packages: "./..."
}

// Run a command with the binary we just built
help: docker.#Run & {
	image: build
	args: ["dagger", "-h"]
}

// Build dagger using the (included) Dockerfile
buildWithDocker: docker.#Build & {
	source: repository
}

// Run a command in the docker image we just built
helpFromDocker: docker.#Run & {
	image: buildWithDocker.image
	args: ["dagger", "-h"]
}
