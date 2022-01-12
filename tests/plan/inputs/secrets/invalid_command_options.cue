package main

import (
	"dagger.io/dagger/engine"
)

engine.#Plan & {
	inputs: secrets: echo: command: {
		name: "cat"
		args: ["--sfgjkhf"] // // should fail because invalid option
	}

	actions: {

		image: engine.#Pull & {
			source: "alpine:3.15.0@sha256:e7d88de73db3d3fd9b2d63aa7f447a10fd0220b7cbf39803c803f2af9ba256b3"
		}

		verify: engine.#Exec & {
			input: image.output
			mounts: secret: {
				dest:     "/run/secrets/test"
				contents: inputs.secrets.echo.contents
			}
			args: [
				"sh", "-c",
				#"""
					test "$(cat /run/secrets/test)" = "hello europa"
					ls -l /run/secrets/test | grep -- "-r--------"
					"""#,
			]
		}
	}
}
