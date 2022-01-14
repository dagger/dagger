package main

import (
	"dagger.io/dagger/engine"
)

engine.#Plan & {
	actions: {
		image: engine.#Pull & {
			source: "alpine:3.15.0@sha256:e7d88de73db3d3fd9b2d63aa7f447a10fd0220b7cbf39803c803f2af9ba256b3"
		}

		source: engine.#Source & {
			path: "."
		}

		exec: engine.#Exec & {
			input: image.output
			mounts: code: {
				dest:     "/src"
				contents: source.output
			}
			args: ["/src/test.sh"]
		}

		verifyHello: engine.#ReadFile & {
			input: source.output
			path:  "/hello.txt"
		} & {
			// assert result
			contents: "hello\n"
		}

		verifyWorld: engine.#ReadFile & {
			input: source.output
			path:  "/world.txt"
		} & {
			// assert result
			contents: "world\n"
		}
	}
}
