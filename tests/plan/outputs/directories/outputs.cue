package main

import (
	"dagger.io/dagger"
)

dagger.#Plan & {
	actions: data: dagger.#WriteFile & {
		input:       dagger.#Scratch
		path:        "/test"
		permissions: 0o600
		contents:    "foobar"
	}

	outputs: directories: test: {
		contents: actions.data.output
		dest:     "./out"
	}
}
