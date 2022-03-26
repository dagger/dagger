package main

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"
)

dagger.#Plan & {
	client: filesystem: "../rootfs": read: {
		contents: dagger.#FS
		include: ["*.txt"]
	}
	actions: test: {
		[string]: core.#ReadFile & {
			input: client.filesystem."../rootfs".read.contents
		}
		valid: {
			path:     "test.txt"
			contents: "local directory"
		}
		notIncluded: path: "test.log"
	}
}
