package main

import (
	"dagger.io/dagger/engine"
	"dagger.io/dagger"
)

engine.#Plan & {
	actions: {
		image: engine.#Pull & {
			source: "alpine:3.15.0@sha256:e7d88de73db3d3fd9b2d63aa7f447a10fd0220b7cbf39803c803f2af9ba256b3"
		}

		mkdir: engine.#Mkdir & {
			input: image.output
			path:  "/test/foo"
		}

		writeChecker: engine.#WriteFile & {
			input:       mkdir.output
			path:        "/test/foo/hello"
			contents:    "world"
			permissions: 0o700
		}

		subdir: dagger.#Subdir & {
			input: writeChecker.output
			path:  "/test/foo"
		}

		verify: engine.#ReadFile & {
			input: subdir.output
			path:  "/hello"
		} & {
			// assert result
			contents: "world"
		}
	}
}
