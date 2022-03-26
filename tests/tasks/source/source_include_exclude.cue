package main

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"
)

dagger.#Plan & {
	actions: {
		image: core.#Pull & {
			source: "alpine:3.15.0@sha256:e7d88de73db3d3fd9b2d63aa7f447a10fd0220b7cbf39803c803f2af9ba256b3"
		}

		sourceInclude: core.#Source & {
			path: "."
			include: ["hello.txt"]
		}

		sourceExclude: core.#Source & {
			path: "."
			exclude: ["hello.txt"]
		}

		test: core.#Exec & {
			input: image.output
			mounts: {
				include: {
					dest:     "/include"
					contents: sourceInclude.output
				}
				exclude: {
					dest:     "/exclude"
					contents: sourceExclude.output
				}
			}

			args: ["sh", "-c",
				#"""
					test "$(find /include/ | wc -l)" -eq 1
					test -f /include/hello.txt
					test ! -f /exclude/hello.txt
					"""#,
			]
		}
	}
}
