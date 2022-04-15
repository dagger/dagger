package go

import (
	"dagger.io/dagger"
	"universe.dagger.io/go"
)

dagger.#Plan & {
	client: filesystem: "./data/hello": read: contents: dagger.#FS

	actions: test: {
		simple: go.#Test & {
			source:  client.filesystem."./data/hello".read.contents
			package: "./greeting"
		}

		withPackages: go.#Test & {
			source: client.filesystem."./data/hello".read.contents
			packages: ["./greeting", "./math"]
		}

	}
}
