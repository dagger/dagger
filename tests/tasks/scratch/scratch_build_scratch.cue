package main

import (
	"dagger.io/dagger"
)

dagger.#Plan & {
	actions: build: dagger.#Build & {
		source: dagger.#Scratch
		dockerfile: contents: "FROM scratch"
		// Assert that output is dagger.#Scratch
		output: dagger.#Scratch
	}
}
