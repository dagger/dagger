package main

import (
	"dagger.io/dagger"

	"universe.dagger.io/alpine"
	"universe.dagger.io/docker"
)

dagger.#Plan & {
	actions: {
		// _build is a hidden action 
		// it builds a base image whose sole purpose is to install all the required packages
		// In below example, create a file that needs to exist in several 
		_build: docker.#Build & {
			// convenient way to chain input-output image state
			steps: [
				// Pull an Alpine image
				alpine.#Build,
				// Create a file inside
				docker.#Run & {
					command: {
						name: "sh"
						flags: "-c": "echo example > /test.txt"
					}
				},
			]
		}

		verify: bash.#Run & {
			// Use as image the state of the `_first` image, after execution
			input: _build.output

			// Context: Make sure action always gets executed (idempotence)
			always: true

			// Show content of file, to see if `#FS` has indeed been shared between the two actions
			script: contents: "ls /test"
		}
	}
}
