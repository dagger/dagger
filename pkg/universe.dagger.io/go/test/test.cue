package go

import (
	"dagger.io/dagger"
	"universe.dagger.io/go"
)

dagger.#Plan & {
	inputs: directories: testhello: path: "./data/hello"

	actions: tests: test: simple: go.#Test & {
		source:  inputs.directories.testhello.contents
		package: "./greeting"
	}
}
