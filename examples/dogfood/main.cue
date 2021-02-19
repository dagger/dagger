package main

import (
	"dagger.io/dagger"
	"dagger.io/go"
)

repository: dagger.#Dir // Use `--input-dir repository=.` from the root directory of the project

build: go.#Build & {
	source:   repository
	packages: "./cmd/dagger"
	output:   "/usr/local/bin/dagger"
}

test: go.#Test & {
	source:   repository
	packages: "./..."
}

help: #dagger: compute: [
	dagger.#Load & {from: build},
	dagger.#Exec & {args: ["dagger", "-h"]},
]
