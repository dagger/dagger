package testing

import (
	"dagger.io/dagger"
)

dagger.#Plan & {
	inputs: directories: testdata: path: "./testdata"

	actions: {
		build: dagger.#Dockerfile & {
			source: inputs.directories.testdata.contents
			dockerfile: path: "./dockerfilepath/Dockerfile.custom"
		}

		verify: dagger.#Exec & {
			input: build.output
			args: ["sh", "-c", "test $(cat /test) = dockerfilePath"]
		}
	}
}
