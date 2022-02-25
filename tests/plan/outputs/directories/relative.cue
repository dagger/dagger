package main

import (
	"dagger.io/dagger"
)

dagger.#Plan & {
	actions: data: dagger.#WriteFile & {
		input:       dagger.#Scratch
		path:        "/test_relative"
		permissions: 0o600
		contents:    "foobar"
	}

	outputs: directories: test_relative: {
		contents: actions.data.output
		dest:     "./out"
	}
}
