package main

import (
	"dagger.io/dagger"
)

dagger.#Plan & {
	actions: {
		write: dagger.#WriteFile & {
			input:       dagger.#Scratch
			path:        "/testing"
			contents:    "1,2,3"
			permissions: 700
		}
		readfile: dagger.#ReadFile & {
			input: write.output
			path:  "/testing"
		} & {
			// assert result
			contents: "1,2,3"
		}
	}
}
