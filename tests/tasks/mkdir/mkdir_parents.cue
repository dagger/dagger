package main

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"
)

dagger.#Plan & {
	actions: {
		image: core.#Pull & {
			source: "alpine:3.15.0@sha256:e7d88de73db3d3fd9b2d63aa7f447a10fd0220b7cbf39803c803f2af9ba256b3"
		}

		mkdir: core.#Mkdir & {
			input: image.output
			path:  "/test/baz"
		}

		writeChecker: core.#WriteFile & {
			input:       mkdir.output
			path:        "/test/baz/foo"
			contents:    "bar"
			permissions: 700
		}

		readChecker: core.#ReadFile & {
			input: writeChecker.output
			path:  "/test/baz/foo"
		} & {
			// assert result
			contents: "bar"
		}
	}
}
