package testing

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"
)

dagger.#Plan & {
	client: filesystem: testdata: read: contents: dagger.#FS

	actions: {
		build: core.#Dockerfile & {
			source: client.filesystem.testdata.read.contents
		}

		verify: core.#Exec & {
			input: build.output
			args: ["sh", "-c", "test $(cat /dir/foo) = foobar"]
		}
	}
}
