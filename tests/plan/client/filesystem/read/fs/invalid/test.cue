package main

import (
	"dagger.io/dagger"
)

dagger.#Plan & {
	client: filesystem: "../rootfs/test.txt": read: contents: dagger.#FS
	actions: test: {
	}
}
