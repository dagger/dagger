package main

import (
	"dagger.io/dagger"
)

dagger.#Plan & {
	client: filesystem: "/foobar": read: contents: dagger.#FS
	actions: test: {
	}
}
