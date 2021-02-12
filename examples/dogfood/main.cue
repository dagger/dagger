package main

import (
	"dagger.io/dagger"
	"dagger.io/go"
)

repository: dagger.#Dir // Use `--input-dir repository=.` from the root directory of the project

// Build `dagger` using Go
build: go.#Build & {
	source:   repository
	packages: "./cmd/dagger"
	output:   "/usr/local/bin/dagger"
}

test: go.#Test & {
	source:   repository
	packages: "./..."
}

// Run a command with the binary we just built
help: #compute: [
	dagger.#Load & {from: build},
	dagger.#Exec & {args: ["dagger", "-h"]},
]

// Build dagger using the (included) Dockerfile
buildWithDocker: #compute: [
	dagger.#DockerBuild & {
		context: repository
	},
]

// Run a command in the docker image we just built
helpFromDocker: #compute: [
	dagger.#Load & {from: buildWithDocker},
	dagger.#Exec & {args: ["dagger", "-h"]},
]
