package main

import (
	"dagger.io/dagger"
)

dagger.#Plan & {
	inputs: directories: test: path: string

	actions: {
		_readFile: dagger.#ReadFile & {
			input: inputs.directories.test.contents
			path:  "test.txt"
		}

		// Test that file exists and contains correct content
		exists: _readFile & {
			contents: "local directory"
		}

		// Test that file does NOT exist
		notExists: _readFile & {
			contents: "local directory"
		}

		// Test that file exists and contains conflicting content
		conflictingValues: _readFile & {
			contents: "local dfsadf"
		}
	}
}
