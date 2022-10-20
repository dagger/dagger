package main

import (
	"dagger.io/dagger"
	"universe.dagger.io/docker"
)

dagger.#Plan & {
	actions: {
		pull: docker.#Pull & {
			source: "alpine"
		}
		// wrap docker.#Push to have more control over the outputs
		push: {
			_op: docker.#Push & {
				image: pull.output
				dest:  "localhost:5042/alpine"
			}

			// The resulting digest
			digest: _op.result

			// The $PATH set in the image
			path: _op.image.config.env.PATH
		}
	}
}
