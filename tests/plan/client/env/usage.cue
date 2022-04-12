package main

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"
)

dagger.#Plan & {
	client: env: {
		TEST_STRING:  string
		TEST_SECRET:  dagger.#Secret
		TEST_DEFAULT: string | *"hello world"
	}
	actions: {
		image: core.#Pull & {
			source: "alpine:3.15.0@sha256:e7d88de73db3d3fd9b2d63aa7f447a10fd0220b7cbf39803c803f2af9ba256b3"
		}
		test: {
			string: core.#Exec & {
				input: image.output
				args: ["test", client.env.TEST_STRING, "=", "foo"]
			}
			default: core.#Exec & {
				input: image.output
				args: ["test", client.env.TEST_DEFAULT, "=", "hello world"]
			}
			secret: core.#Exec & {
				input: image.output
				mounts: secret: {
					dest:     "/run/secrets/test"
					contents: client.env.TEST_SECRET
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
