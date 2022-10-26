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

		exec: core.#Exec & {
			input: image.output
			mounts: fs: {
				dest:     "/scratch"
				contents: dagger.#Scratch
			}
			workdir: "/"
			args: [
				"sh", "-c",
				#"""
					test "$(ls -A /)" != ""
					test "$(ls -A /scratch)" = ""
					"""#,
			]
		}
	}
}
