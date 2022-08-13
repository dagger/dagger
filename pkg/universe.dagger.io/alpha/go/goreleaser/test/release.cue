package goreleaser

import (
	"dagger.io/dagger"

	"universe.dagger.io/alpha/go/goreleaser"
)

dagger.#Plan & {
	client: filesystem: "./data/hello": read: contents: dagger.#FS

	actions: test: {
		simple: build: goreleaser.#Release & {
			source: client.filesystem."./data/hello".read.contents

			dryRun:   true
			snapshot: true
		}

		customImage: build: goreleaser.#ReleaseBase & {
			source: client.filesystem."./data/hello".read.contents

			_image: goreleaser.#Image & {
				tag: "v1.9.2"
			}
			image: _image.output

			dryRun:   true
			snapshot: true
		}
	}
}
