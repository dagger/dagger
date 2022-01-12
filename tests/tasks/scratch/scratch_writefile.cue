package main

import (
	"dagger.io/dagger/engine"
)

engine.#Plan & {
	actions: {
		write: engine.#WriteFile & {
			input:       engine.#Scratch
			path:        "/testing"
			contents:    "1,2,3"
			permissions: 700
		}
		readfile: engine.#ReadFile & {
			input: write.output
			path:  "/testing"
		} & {
			// assert result
			contents: "1,2,3"
		}
	}
}
