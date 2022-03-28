package main

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"
)

dagger.#Plan & {
	actions: test: {
		_image: core.#Pull & {
			source: "alpine:3.15.0@sha256:e7d88de73db3d3fd9b2d63aa7f447a10fd0220b7cbf39803c803f2af9ba256b3"
		}

		_exec: core.#Exec & {
			input: _image.output
			args: ["sh", "-c", "echo -n Hello Europa > /out.txt"]
		}

		_verify: core.#ReadFile & {
			input: _exec.output
			path:  "/out.txt"
		} & {
			// assert result
			contents: "Hello Europa"
		}
	}
}
