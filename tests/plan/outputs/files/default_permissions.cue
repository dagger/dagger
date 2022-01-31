package main

import "dagger.io/dagger"

dagger.#Plan & {
	outputs: files: test: {
		contents: "foobar"
		dest:     "./test"
	}
}
