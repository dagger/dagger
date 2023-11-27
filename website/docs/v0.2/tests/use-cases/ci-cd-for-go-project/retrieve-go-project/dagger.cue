package main

import (
	"dagger.io/dagger"
)

dagger.#Plan & {
	// Retrieve go source code
	client: filesystem: ".": read: {
		contents: dagger.#FS
		include: ["go.mod", "go.sum", "**/*.go"]
	}

	actions: {
		// Alias to code directory
		_code: client.filesystem.".".read.contents
	}
}
