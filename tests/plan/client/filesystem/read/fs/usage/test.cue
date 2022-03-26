package main

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"
)

dagger.#Plan & {
	client: filesystem: rootfs: read: {
		contents: dagger.#FS
		exclude: ["*.log"]
	}
	actions: test: {
		[string]: core.#ReadFile & {
			input: client.filesystem.rootfs.read.contents
		}
		valid: {
			path:     "test.txt"
			contents: "local directory"
		}
		conflictingValues: {
			path:     "test.txt"
			contents: "local foobar"
		}
		excluded: path:  "test.log"
		notExists: path: "test.json"
	}
}
