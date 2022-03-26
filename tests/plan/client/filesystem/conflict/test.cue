package main

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"
)

dagger.#Plan & {
	client: filesystem: "test.txt": {
		// no dependencies between these two, one must be forced
		read: contents:  string
		write: contents: actions.test.export.contents
	}
	actions: {
		image: core.#Pull & {
			source: "alpine:3.15.0@sha256:e7d88de73db3d3fd9b2d63aa7f447a10fd0220b7cbf39803c803f2af9ba256b3"
		}
		test: {
			read: core.#Exec & {
				input: image.output
				args: ["echo", client.filesystem."test.txt".read.contents]
			}
			write: core.#Exec & {
				input: image.output
				args: ["sh", "-c",
					#"""
						echo -n bar > /out.txt
						"""#,
				]
			}
			export: core.#ReadFile & {
				input: write.output
				path:  "out.txt"
			}
		}
	}
}
