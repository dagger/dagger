package main

import (
	"dagger.io/dagger/core"
)

_source: core.#Source & {
	path: "."
	exclude: [
		"ci",
		"node_modules",
		"cmd/dagger/dagger",
		"cmd/dagger/dagger-debug",
	]
}
