package main

import (
	"dagger.io/dagger"
)

dagger.#Plan & {
	// Reading a file into a dagger.#FS should not be possbile
	client: filesystem: "../rootfs/test.txt": read: contents: dagger.#FS
	actions: test: {
	}
}
