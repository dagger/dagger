package main

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"
)

dagger.#Plan & {
	// Reading a file into a dagger.#FS should not be possbile
	client: filesystem: "../rootfs/test.txt": read: contents: dagger.#FS
	actions: test: core.#Nop & {
		input: client.filesystem."../rootfs/test.txt".read.contents
	}
}
