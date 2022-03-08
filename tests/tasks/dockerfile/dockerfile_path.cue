package testing

import (
	"dagger.io/dagger"
)

dagger.#Plan & {
	client: filesystem: testdata: read: contents: dagger.#FS

	actions: {
		build: dagger.#Dockerfile & {
			source: client.filesystem.testdata.read.contents
			dockerfile: path: "./dockerfilepath/Dockerfile.custom"
		}

		verify: dagger.#Exec & {
			input: build.output
			args: ["sh", "-c", "test $(cat /test) = dockerfilePath"]
		}
	}
}
