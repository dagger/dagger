package main

import (
	"dagger.io/dagger"
	"universe.dagger.io/go"
)

dagger.#Plan & {
	client: {
		// Retrieve go source code
		filesystem: ".": read: {
			contents: dagger.#FS
			include: ["go.mod", "go.sum", "**/*.go"]
		}
	}

	actions: {
		// Alias to code directory
		_code: client.filesystem.".".read.contents

		// Improved go base image with useful tool
		// Enable cgo by installing build-base
		_base: go.#Image & {
			packages: "build-base": version: _
		}

		// Build go project
		build: go.#Build & {
			source: _code
		}
	}
}
