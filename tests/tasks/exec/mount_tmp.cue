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
			mounts: temp: {
				dest:     "/temp"
				contents: core.#TempDir
			}
			args: [
				"sh", "-c",
				#"""
					echo -n hello world > /temp/output.txt
					"""#,
			]
		}

		verify: core.#Exec & {
			input: exec.output
			args: [
				"sh", "-c",
				#"""
					test ! -f /temp/output.txt
					"""#,
			]
		}
	}
}
