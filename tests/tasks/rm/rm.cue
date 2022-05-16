package main

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"
)

dagger.#Plan & {
	actions: {
		pull: core.#Pull & {
			source: "alpine:3.15.0@sha256:e7d88de73db3d3fd9b2d63aa7f447a10fd0220b7cbf39803c803f2af9ba256b3"
		}

		test: {
			rmFile: {
				// Write a new file
				write: core.#WriteFile & {
					input:    pull.output
					path:     "/test.txt"
					contents: "1,2,3"
				}

				// Remove file
				rm: core.#Rm & {
					input: write.output
					path:  write.path
				}

				verify: core.#Exec & {
					input: rm.output
					args: ["/bin/sh", "-e", "-c", """
						test ! -e /test.txt
						"""]
				}
			}

			rmWildcard: {
				// Write directory
				write: core.#Exec & {
					input: pull.output
					args: ["/bin/sh", "-e", "-c", """
						touch /foo.txt
						touch /bar.txt
						touch /data.json
						"""]
				}

				// Remove all .txt file
				rm: core.#Rm & {
					input: write.output
					path:  "/*.txt"
				}

				verify: core.#Exec & {
					input: rm.output
					args: ["/bin/sh", "-e", "-c", """
							test ! -e /foo.txt
							test ! -e /bar.txt
							test -e /data.json
						"""]
				}
			}

			rmDir: {
				// Write directory
				write: core.#Exec & {
					input: pull.output
					args: ["/bin/sh", "-e", "-c", """
						mkdir -p /test
						touch /test/foo.txt
						touch /test/bar.txt
						"""]
				}

				// Remove directory
				rm: core.#Rm & {
					input: write.output
					path:  "/test"
				}

				verify: core.#Exec & {
					input: rm.output
					args: ["/bin/sh", "-e", "-c", """
							test ! -e /test
						"""]
				}
			}

			notAllowWildcard: {
				// Write file
				write: core.#Exec & {
					input: pull.output
					args: ["/bin/sh", "-e", "-c", """
						touch '/*.txt'
						touch /bar.txt
						"""]
				}

				rm: core.#Rm & {
					input:         write.output
					path:          "/*.txt"
					allowWildcard: false
				}

				verify: core.#Exec & {
					input: rm.output
					args: ["/bin/sh", "-e", "-c", """
							ls -l
							test ! -e '/*.txt'
							test -e /bar.txt # bar.txt should exist
						"""]
				}
			}
		}
	}
}
