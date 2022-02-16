package testing

import (
	"dagger.io/dagger"
)

dagger.#Plan & {
	inputs: directories: testdata: path: "./testdata"

	actions: {
		build: dagger.#Dockerfile & {
			source: inputs.directories.testdata.contents
		}

		verify: dagger.#Exec & {
			input: build.output
			args: ["sh", "-c", "test $(cat /dir/foo) = foobar"]
		}
	}
}
