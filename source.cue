package main

import (
	"dagger.io/dagger"
)

_source: dagger.#Source & {
	path: "."
	exclude: [
		"ci",
		"node_modules",
		"cmd/dagger/dagger",
		"cmd/dagger/dagger-debug",
	]
}
