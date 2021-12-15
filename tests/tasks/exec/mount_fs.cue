package main

import (
	"alpha.dagger.io/europa/dagger/engine"
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
					echo -n hello world > /output.txt
					"""#,
			]
		}

		verify: engine.#Exec & {
			input: image.output
			mounts: fs: {
				dest:     "/target"
				contents: exec.output
			}
			args: [
				"sh", "-c",
				#"""
					test "$(cat /target/output.txt)" = "hello world"
					"""#,
			]
		}
	}
}
