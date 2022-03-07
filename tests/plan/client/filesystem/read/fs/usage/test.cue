package main

import (
	"dagger.io/dagger"
)

dagger.#Plan & {
	client: filesystem: rootfs: read: {
		contents: dagger.#FS
		exclude: ["*.log"]
	}
	actions: test: {
		[string]: dagger.#ReadFile & {
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
