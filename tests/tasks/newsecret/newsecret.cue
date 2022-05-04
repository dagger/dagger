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

		generate: core.#Exec & {
			input: image.output
			args: ["sh", "-c", "echo test > /secret && touch /emptySecret"]
		}

		load: core.#NewSecret & {
			input: generate.output
			path:  "/secret"
		}

		loadEmpty: core.#NewSecret & {
			input: generate.output
			path:  "/emptySecret"
		}

		verify: core.#Exec & {
			input: image.output
			mounts: {
				secret: {
					dest:     "/run/secrets/test"
					contents: load.output
				}
				emptySecret: {
					dest:     "/run/secrets/emptyTest"
					contents: loadEmpty.output
				}
			}
			args: [
				"sh", "-c",
				#"""
					test "$(cat /run/secrets/test)" = "test"
					test "$(cat /run/secrets/emptyTest)" = ""
					echo HELLOWORLD
					"""#,
			]
		}
	}
}
