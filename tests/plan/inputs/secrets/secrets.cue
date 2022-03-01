package main

import (
	"dagger.io/dagger"
)

dagger.#Plan & {
	inputs: secrets: {
		echo: command: {
			name: "echo"
			args: ["hello europa"]
		}

		relative: command: {
			name: "cat"
			args: ["./test.txt"]
		}

		badCommand: command: {
			name: "rtyet" // should fail because command doesn't exist
			args: ["hello europa"]
		}

		badArgs: command: {
			name: "cat"
			args: ["--sfgjkhf"] // // should fail because invalid option
		}
	}

	actions: {

		_image: dagger.#Pull & {
			source: "alpine:3.15.0@sha256:e7d88de73db3d3fd9b2d63aa7f447a10fd0220b7cbf39803c803f2af9ba256b3"
		}

		test: {

			[string]: dagger.#Exec & {
				input: _image.output
				mounts: secret: {
					dest: "/run/secrets/test"
					// contents: inputs.secrets.echo.contents
				}
				args: [
					"sh", "-c",
					#"""
						test "$(cat /run/secrets/test)" = "hello europa"
						ls -l /run/secrets/test | grep -- "-r--------"
						"""#,
				]
			}

			valid: mounts: secret: contents:      inputs.secrets.echo.contents
			relative: mounts: secret: contents:   inputs.secrets.relative.contents
			badCommand: mounts: secret: contents: inputs.secrets.badCommand.contents
			badArgs: mounts: secret: contents:    inputs.secrets.badArgs.contents
		}
	}
}
