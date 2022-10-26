package main

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"
)

dagger.#Plan & {
	actions: {
		params: foo?: string

		_image: core.#Pull & {
			source: "alpine:3.15.0@sha256:e7d88de73db3d3fd9b2d63aa7f447a10fd0220b7cbf39803c803f2af9ba256b3"
		}

		test: {
			[string]: core.#Exec & {
				input: _image.output
				env: FOO: string
				args: [
					"sh", "-c",
					#"""
						test -n "$FOO"
						"""#,
				]
			}

			direct: env: FOO: *"direct" | string
			if params.foo != _|_ {
				"params": env: FOO: params.foo
			}
		}
	}
}
