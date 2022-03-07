package main

import (
	"strings"
	"dagger.io/dagger"
)

dagger.#Plan & {
	client: commands: {
		normal: {
			name: "echo"
			args: ["hello europa"]
		}
		relative: {
			name: "cat"
			args: ["./test.txt"]
		}
		secret: {
			name:   "tee"
			stdout: dagger.#Secret
			stdin:  "hello secretive europa"
		}
		error: {
			name: "sh"
			flags: "-c": ">&2 echo 'error'"
			stderr: string
		}
		invalid: name: "foobar"
	}
	actions: {
		image: dagger.#Pull & {
			source: "alpine:3.15.0@sha256:e7d88de73db3d3fd9b2d63aa7f447a10fd0220b7cbf39803c803f2af9ba256b3"
		}
		test: {
			invalid: dagger.#Exec & {
				input: image.output
				args: ["echo", client.commands.invalid.stdout]
			}
			valid: {
				normal: dagger.#Exec & {
					input: image.output
					args: ["test", strings.TrimSpace(client.commands.normal.stdout), "=", "hello europa"]
				}
				relative: dagger.#Exec & {
					input: image.output
					args: ["test", strings.TrimSpace(client.commands.relative.stdout), "=", "test"]
				}
				error: dagger.#Exec & {
					input: image.output
					args: ["test", strings.TrimSpace(client.commands.error.stderr), "=", "error"]
				}
				secret: dagger.#Exec & {
					input: image.output
					mounts: secret: {
						dest:     "/run/secrets/test"
						contents: client.commands.secret.stdout
					}
					args: [
						"sh", "-c",
						#"""
						test "$(cat /run/secrets/test)" = "hello secretive europa"
						"""#,
					]
				}
			}
		}
	}
}
