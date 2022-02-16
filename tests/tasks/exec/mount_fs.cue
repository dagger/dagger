package main

import (
	"dagger.io/dagger"
)

dagger.#Plan & {
	actions: {
		image: dagger.#Pull & {
			source: "alpine:3.15.0@sha256:e7d88de73db3d3fd9b2d63aa7f447a10fd0220b7cbf39803c803f2af9ba256b3"
		}

		exec: dagger.#Exec & {
			input: image.output
			args: [
				"sh", "-c",
				#"""
					echo -n hello world > /output.txt
					"""#,
			]
		}

		verify: dagger.#Exec & {
			input: image.output
			mounts: fs: {
				dest:     "/target"
				contents: exec.output
			}
			args: [
				"sh", "-c",
				#"""
					test "$(cat /target/output.txt)" = "hello world"
					touch /target/rw
					"""#,
			]
		}

		verifyRO: dagger.#Exec & {
			input: image.output
			mounts: fs: {
				dest:     "/target"
				contents: exec.output
				ro:       true
			}
			args: [
				"sh", "-c",
				#"""
					test "$(cat /target/output.txt)" = "hello world"

					touch /target/ro && exit 1
					true
					"""#,
			]
		}

		verifySource: dagger.#Exec & {
			input: image.output
			mounts: fs: {
				dest:     "/target.txt"
				contents: exec.output
				source:   "/output.txt"
			}
			args: [
				"sh", "-c",
				#"""
					test "$(cat /target.txt)" = "hello world"
					"""#,
			]
		}
	}
}
