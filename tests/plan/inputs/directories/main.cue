package main

import (
	"dagger.io/dagger"
)

dagger.#Plan & {
	inputs: directories: test: path: string

	actions: {
		// Test that file exists and contains correct content
		exists: dagger.#ReadFile & {
			input:    inputs.directories.test.contents
			path:     "test.txt"
			contents: "local directory"
		}

		// Test that file does NOT exist
		notExists: dagger.#ReadFile & {
			input:    inputs.directories.test.contents
			path:     "test.txt"
			contents: "local directory"
		}

		// Test that file exists and contains conflicting content
		conflictingValues: dagger.#ReadFile & {
			input:    inputs.directories.test.contents
			path:     "test.txt"
			contents: "local dfsadf"
		}
	}
}
