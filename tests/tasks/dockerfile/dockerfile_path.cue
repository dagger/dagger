package testing

import (
	"dagger.io/dagger/engine"
)

engine.#Plan & {
	inputs: directories: testdata: path: "./testdata"

	actions: {
		build: engine.#Dockerfile & {
			source: inputs.directories.testdata.contents
			path:   "./dockerfilepath/Dockerfile.custom"
		}

		verify: engine.#Exec & {
			input: build.output
			args: ["sh", "-c", "test $(cat /test) = dockerfilePath"]
		}
	}
}
