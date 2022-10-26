package main

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"
)

dagger.#Plan & {
	actions: test: image: core.#Pull & {
		source: "alpine:3.15.0"
	}
}
