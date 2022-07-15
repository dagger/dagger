package main

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"
)

dagger.#Plan & {
	actions: pull: core.#Pull & {
		source: "unknownimage"
	}
}
