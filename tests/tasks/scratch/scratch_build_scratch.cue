package main

import (
	"dagger.io/dagger/engine"
)

engine.#Plan & {
	actions: build: engine.#Build & {
		source: engine.#Scratch
		dockerfile: contents: "FROM scratch"
		// Assert that output is engine.#Scratch
		output: engine.#Scratch
	}
}
