package main

import (
	"dagger.io/dagger"
)

dagger.#Plan & {
	actions: {
		image: dagger.#Pull & {
			source: "alpine:3.15.0@sha256:e7d88de73db3d3fd9b2d63aa7f447a10fd0220b7cbf39803c803f2af9ba256b3"
		}

		addUser: dagger.#Exec & {
			input: image.output
			args: ["adduser", "-D", "test"]
		}

		verifyUsername: dagger.#Exec & {
			input: addUser.output
			user:  "test"
			args: [
				"sh", "-c",
				#"""
					test "$(whoami)" = "test"
					"""#,
			]
		}

		verifyUserID: dagger.#Exec & {
			input: addUser.output
			user:  "1000"
			args: [
				"sh", "-c",
				#"""
					test "$(whoami)" = "test"
					"""#,
			]
		}

	}
}
