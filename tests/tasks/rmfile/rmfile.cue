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
			// Write a new file
			write: core.#WriteFile & {
				input:    pull.output
				path:     "/test.txt"
				contents: "1,2,3"
			}

			// Remove file
			rm: core.#RmFile & {
				input: write.output
				path:  write.path
			}

			verify: core.#Exec & {
				input: rm.output
				args: ["/bin/sh", "-c", """
					if !(cat /test.txt); then true ; else false; fi
					"""]
			}
		}
	}
}
