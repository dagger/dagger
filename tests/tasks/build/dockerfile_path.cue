package testing

import (
	"alpha.dagger.io/europa/dagger/engine"
)

engine.#Plan & {
	inputs: directories: testdata: path: "./testdata"

	actions: {
		build: engine.#Build & {
			source: inputs.directories.testdata.contents
			dockerfile: path: "./dockerfilepath/Dockerfile.custom"
		}

		verify: engine.#Exec & {
			input: build.output
			args: ["sh", "-c", "test $(cat /test) = dockerfilePath"]
		}
	}
}
