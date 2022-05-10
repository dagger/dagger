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
			// Write directory
			write: core.#Exec & {
				input: pull.output
				args: ["/bin/sh", "-c", """
					touch /foo.txt
					touch /bar.txt
					touch /data.json
					"""]
			}

			// Remove all .txt file
			rm: core.#RmFile & {
				input: write.output
				path:  "/*.txt"
			}

			verify: core.#Exec & {
				input: rm.output
				args: ["/bin/sh", "-c", """
					if !(cat /foo.txt); then true ; else false; fi
					if !(cat /bar.txt); then true ; else false; fi
					cat data.json
					"""]
			}
		}
	}
}
