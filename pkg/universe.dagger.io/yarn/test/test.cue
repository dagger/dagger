package yarn

import (
	"dagger.io/dagger"
	"dagger.io/dagger/engine"

	"universe.dagger.io/yarn"
)

dagger.#Plan & {
	inputs: directories: {
		testdata: path:  "./data/foo"
		testdata2: path: "./data/bar"
	}

	actions: tests: {

		// Run yarn.#Build
		simple: {
			build: yarn.#Build & {
				source: inputs.directories.testdata.contents
			}

			verify: #AssertFile & {
				input:    build.output
				path:     "test"
				contents: "output\n"
			}
		}

		// Run yarn.#Build with a custom name
		customName: {
			build: yarn.#Build & {
				name:   "My Build"
				source: inputs.directories.testdata.contents
			}
			verify: #AssertFile & {
				input:    build.output
				path:     "test"
				contents: "output\n"
			}
		}
	}
}

// Make an assertion on the contents of a file
#AssertFile: {
	input:    dagger.#FS
	path:     string
	contents: string

	_read: engine.#ReadFile & {
		"input": input
		"path":  path
	}

	actual: _read.contents

	// Assertion
	contents: actual
}
