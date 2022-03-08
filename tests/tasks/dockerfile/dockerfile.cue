package testing

import (
	"dagger.io/dagger"
)

dagger.#Plan & {
	client: filesystem: testdata: read: contents: dagger.#FS

	actions: {
		build: dagger.#Dockerfile & {
			source: client.filesystem.testdata.read.contents
		}

		verify: dagger.#Exec & {
			input: build.output
			args: ["sh", "-c", "test $(cat /dir/foo) = foobar"]
		}
	}
}
