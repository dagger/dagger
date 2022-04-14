package main

import (
	"dagger.io/dagger"

	"universe.dagger.io/go"
)

dagger.#Plan & {
	client: filesystem: "./hello": read: contents: dagger.#FS

	actions: {
		test: go.#Test & {
			source:  client.filesystem."./hello".read.contents
			package: "./..."
		}

		build: go.#Build & {
			source: client.filesystem."./hello".read.contents
		}
	}

}
