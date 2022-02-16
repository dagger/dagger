package main

import (
	"dagger.io/dagger"
)

dagger.#Plan & {
	inputs: secrets: testSecret: envvar: "TESTSECRET"
	actions: {
		image: dagger.#Pull & {
			source: "alpine:3.15.0@sha256:e7d88de73db3d3fd9b2d63aa7f447a10fd0220b7cbf39803c803f2af9ba256b3"
		}

		verify: dagger.#Exec & {
			input: image.output
			mounts: secret: {
				dest:     "/run/secrets/test"
				contents: inputs.secrets.testSecret.contents
			}
			args: [
				"sh", "-c",
				#"""
					test "$(cat /run/secrets/test)" = "hello world"
					ls -l /run/secrets/test | grep -- "-r--------"
					"""#,
			]
		}

		verifyPerm: dagger.#Exec & {
			input: image.output
			mounts: secret: {
				dest:     "/run/secrets/test"
				contents: inputs.secrets.testSecret.contents
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
