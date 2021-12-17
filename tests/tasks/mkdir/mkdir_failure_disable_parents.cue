package main

import (
	"alpha.dagger.io/europa/dagger/engine"
)

engine.#Plan & {
	actions: {
		image: engine.#Pull & {
			source: "alpine:3.15.0@sha256:e7d88de73db3d3fd9b2d63aa7f447a10fd0220b7cbf39803c803f2af9ba256b3"
		}

		mkdir: engine.#Mkdir & {
			input: image.output
			path: "/test/baz"
			parents: false
		}

		writeChecker: engine.#WriteFile & {
			input:    mkdir.output
			path:     "/test/baz/foo"
			contents: "bar"
			mode:     700
		}

		readChecker: engine.#ReadFile & {
			input: writeChecker.output
			path: "/test/baz/foo"
		} & {
			// assert result
			contents: "bar"
		}
	}
}