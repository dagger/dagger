package main

import (
	"dagger.io/dagger"
)

dagger.#Plan & {
	client: filesystem: "../rootfs": read: {
		contents: dagger.#FS
		include: ["*.txt"]
	}
	actions: test: {
		[string]: dagger.#ReadFile & {
			input: client.filesystem."../rootfs".read.contents
		}
		valid: {
			path:     "test.txt"
			contents: "local directory"
		}
		notIncluded: path: "test.log"
	}
}
