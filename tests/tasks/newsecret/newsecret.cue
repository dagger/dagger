package main

import (
	"dagger.io/dagger/engine"
)

engine.#Plan & {
	actions: {
		image: engine.#Pull & {
			source: "alpine:3.15.0@sha256:e7d88de73db3d3fd9b2d63aa7f447a10fd0220b7cbf39803c803f2af9ba256b3"
		}

		generate: engine.#Exec & {
			input: image.output
			args: ["sh", "-c", "echo test > /secret"]
		}

		load: engine.#NewSecret & {
			input: generate.output
			path:  "/secret"
		}

		verify: engine.#Exec & {
			input: image.output
			mounts: secret: {
				dest:     "/run/secrets/test"
				contents: load.output
			}
			args: [
				"sh", "-c",
				#"""
					test "$(cat /run/secrets/test)" = "test"
					"""#,
			]
		}
	}
}
