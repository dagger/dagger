package ci

import (
	"dagger.io/dagger"
	"universe.dagger.io/go"
)

dagger.#Plan & {
	// Retrieve source from current directory
	// Input
	client: filesystem: ".": read: {
		contents: dagger.#FS
		// Include only contents we need to build our project
		// Any other files or patterns can be added
		include: ["**/*.go", "go.mod", "go.sum"]
	}

	// Output
	client: filesystem: "./output": write: contents: actions.build.output

	actions: {
		// Alias on source
		_source: client.filesystem.".".read.contents

		// Build go binary
		build: go.#Build & {
			source: _source
		}
	}
}
