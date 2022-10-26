package main

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"
)

dagger.#Plan & {
	client: env: TESTSECRET: dagger.#Secret
	actions: {
		image: core.#Pull & {
			source: "alpine:3.15.0@sha256:e7d88de73db3d3fd9b2d63aa7f447a10fd0220b7cbf39803c803f2af9ba256b3"
		}

		test: {
			verify: core.#Exec & {
				input: image.output
				mounts: secret: {
					dest:     "/run/secrets/test"
					contents: client.env.TESTSECRET
				}
				args: [
					"sh", "-c",
					#"""
						test "$(cat /run/secrets/test)" = "hello world"
						ls -l /run/secrets/test | grep -- "-r--------"
						"""#,
				]
			}

			verifyPerm: core.#Exec & {
				input: image.output
				mounts: secret: {
					dest:     "/run/secrets/test"
					contents: client.env.TESTSECRET
					uid:      42
					gid:      24
					mask:     0o666
				}
				args: [
					"sh", "-c",
					#"""
						ls -l /run/secrets/test | grep -- "-rw-rw-rw-"
						ls -l /run/secrets/test | grep -- "42"
						ls -l /run/secrets/test | grep -- "24"
						"""#,
				]
			}
		}
	}
}
