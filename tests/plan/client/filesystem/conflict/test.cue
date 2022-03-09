package main

import (
	"dagger.io/dagger"
)

dagger.#Plan & {
	client: filesystem: "test.txt": {
		// no dependencies between these two, one must be forced
		read: contents:  string
		write: contents: actions.test.export.contents
	}
	actions: {
		image: dagger.#Pull & {
			source: "alpine:3.15.0@sha256:e7d88de73db3d3fd9b2d63aa7f447a10fd0220b7cbf39803c803f2af9ba256b3"
		}
		test: {
			read: dagger.#Exec & {
				input: image.output
				args: ["echo", client.filesystem."test.txt".read.contents]
			}
			write: dagger.#Exec & {
				input: image.output
				args: ["sh", "-c",
					#"""
						echo -n bar > /out.txt
						"""#,
				]
			}
			export: dagger.#ReadFile & {
				input: write.output
				path:  "out.txt"
			}
		}
	}
}
