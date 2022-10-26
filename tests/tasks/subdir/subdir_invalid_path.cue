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

		mkdir: core.#Mkdir & {
			input: image.output
			path:  "/test/foo"
		}

		writeChecker: core.#WriteFile & {
			input:       mkdir.output
			path:        "/test/foo/hello"
			contents:    "world"
			permissions: 0o700
		}

		subdir: core.#Subdir & {
			input: writeChecker.output
			path:  "/directorynotfound"
		}

		verify: core.#Exec & {
			input: image.output
			mounts: fs: {
				dest:     "/target"
				contents: subdir.output
			}
			args: [
				"sh", "-c",
				#"""
					test $(ls /target | wc -l) = 1
					"""#,
			]
		}
	}
}
