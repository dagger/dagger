package main

import "alpha.dagger.io/europa/dagger/engine"

engine.#Plan & {
	actions: data: engine.#WriteFile & {
		input:       engine.#Scratch
		path:        "/test"
		permissions: 0o600
		contents:    "foobar"
	}

	outputs: directories: test: {
		contents: actions.data.output
		dest:     "./out"
	}
}
