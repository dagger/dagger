package main

import (
	"dagger.io/dagger"

	"universe.dagger.io/bash"
)

dagger.#Plan & {
	// Path may be absolute, or relative to current working directory
	// Relative path is relative to the dagger CLI position
	client: filesystem: ".": read: {
		// Load the '.' directory (host filesystem) into dagger's runtime
		// Specify to Dagger runtime that it is a `dagger.#FS`
		contents: dagger.#FS
	}

	actions: {
		// Use the bash package to list the content of the local filesystem
		list: bash.#RunSimple & {
			// Script to execute
			script: contents: """
				ls -l /tmp/example
				"""

			// Context: Make sure action always gets executed (idempotence)
			always: true

			// Mount the client FS into the container used by the `bash` package
			mounts: "Local FS": {
				// Path key has to reference the client filesystem you read '.' above
				contents: client.filesystem.".".read.contents
				// Where to mount the FS, in your container image
				dest: "/tmp/example"
			}
		}
	}
}
