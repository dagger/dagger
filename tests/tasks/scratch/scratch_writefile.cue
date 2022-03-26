package main

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"
)

dagger.#Plan & {
	actions: {
		write: core.#WriteFile & {
			input:       dagger.#Scratch
			path:        "/testing"
			contents:    "1,2,3"
			permissions: 700
		}
		readfile: core.#ReadFile & {
			input: write.output
			path:  "/testing"
		} & {
			// assert result
			contents: "1,2,3"
		}
	}
}
