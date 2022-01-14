package main

import (
	"dagger.io/dagger/engine"
)

engine.#Plan & {
	actions: {
		image: engine.#Pull & {
			source: "alpine:3.15.0@sha256:e7d88de73db3d3fd9b2d63aa7f447a10fd0220b7cbf39803c803f2af9ba256b3"
		}

		sourceInclude: engine.#Source & {
			path: "."
			include: ["hello.txt"]
		}

		sourceExclude: engine.#Source & {
			path: "."
			exclude: ["hello.txt"]
		}

		test: engine.#Exec & {
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
