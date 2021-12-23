package main

import "alpha.dagger.io/europa/dagger/engine"

engine.#Plan & {
	actions: {
		scratch: engine.#Scratch

		data: engine.#WriteFile & {
			input:       scratch.output
			path:        "/test"
			permissions: 0o600
			contents:    "foobar"
		}
	}

	outputs: directories: test: {
		contents: actions.data.output
		dest:     "./out"
	}
}
