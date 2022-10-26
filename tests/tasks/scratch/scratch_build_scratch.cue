package main

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"
)

dagger.#Plan & {
	actions: build: core.#Dockerfile & {
		source: dagger.#Scratch
		dockerfile: contents: "FROM scratch"
		// Assert that output is dagger.#Scratch
		output: dagger.#Scratch
	}
}
