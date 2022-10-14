package main

import (
	"dagger.io/dagger"

	"universe.dagger.io/bash"
)

dagger.#Plan & {
	actions: {
		// Create hidden action (not present in `dagger do`)
		// This action runs a bash command on a container image
		// The bash command creates a file
		_first: bash.#RunSimple & {
			// Run bash command to create a file
			script: contents: """
				echo example > /tmp/test
				"""
		}

		// Use as an input image the output the the `_first` action
		test: bash.#Run & {
			// Use as image the state of the `_first` image, after execution
			input: _first.output

			// Context: Make sure action always gets executed (idempotence)
			always: true

			// Show content of file, to see if `#FS` has indeed been shared between the two action
			script: contents: "cat /tmp/test"
		}
	}
}
