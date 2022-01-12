package main

import (
	"dagger.io/dagger/engine"
)

engine.#Plan & {
	actions: {
		image: engine.#Pull & {
			source: "alpine:3.15.0@sha256:e7d88de73db3d3fd9b2d63aa7f447a10fd0220b7cbf39803c803f2af9ba256b3"
		}

		exec: engine.#Exec & {
			input: image.output
			args: [
				"sh", "-c",
				#"""
					echo -n hello world from dagger > /output.txt
					"""#,
			]
		}

		verify_file: engine.#ReadFile & {
			input: exec.output
			path:  "/output.txt"
		} & {
			// assert result
			contents: "hello world from dagger"
		}

		copy: engine.#Copy & {
			input: image.output
			source: {
				root: exec.output
				path: "/output.txt"
			}
			dest: "/output.txt"
		}
		verify_copy: engine.#ReadFile & {
			input: copy.output
			path:  "/output.txt"
		} & {
			// assert result
			contents: "hello world"
		}
	}
}
