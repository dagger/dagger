package main

import (
	"alpha.dagger.io/europa/dagger/engine"
)

engine.#Plan & {
	actions: {
		write: engine.#WriteFile & {
			input:       engine.#Scratch
			path:        "/.dockerignore"
			contents:    "Dockerfile"
			permissions: 700
		}

		build: engine.#Build & {
			source: write.output
			dockerfile: contents: """
				FROM scratch
				"""
			// Assert that output is engine.#Scratch
			output: engine.#Scratch
		}
	}
}
