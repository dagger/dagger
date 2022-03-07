package main

import (
	"dagger.io/dagger"
)

dagger.#Plan & {
	client: filesystem: {
		"cmd.sh": read: contents:     "env"
		"test.txt": read: contents:   string
		"secret.txt": read: contents: dagger.#Secret
	}
	actions: {
		image: dagger.#Pull & {
			source: "alpine:3.15.0@sha256:e7d88de73db3d3fd9b2d63aa7f447a10fd0220b7cbf39803c803f2af9ba256b3"
		}
		test: {
			concrete: dagger.#Exec & {
				input: image.output
				args: ["sh", "-c", client.filesystem."cmd.sh".read.contents]
			}
			usage: {
				string: dagger.#Exec & {
					input: image.output
					args: ["test", client.filesystem."test.txt".read.contents, "=", "foo"]
				}
				secret: dagger.#Exec & {
					input: image.output
					mounts: secret: {
						dest:     "/run/secrets/test"
						contents: client.filesystem."secret.txt".read.contents
					}
					args: [
						"sh", "-c",
						#"""
						test "$(cat /run/secrets/test)" = "bar"
						ls -l /run/secrets/test | grep -- "-r--------"
						"""#,
					]
				}
			}
		}
	}
}
