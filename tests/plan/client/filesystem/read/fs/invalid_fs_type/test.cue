package main

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"
)

dagger.#Plan & {
	// Reading a directory into a non-fs should fail
	client: filesystem: "../rootfs": read: contents: string
	actions: test: core.#Nop & {
		input: client.filesystem."../rootfs".read.contents
	}
}
