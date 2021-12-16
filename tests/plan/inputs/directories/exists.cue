package main

import (
	"alpha.dagger.io/europa/dagger"
	"alpha.dagger.io/europa/dagger/engine"
)

dagger.#Plan & {
	input: directories: test: path: "./plan/inputs/directories"
	actions: verify: engine.#ReadFile & {
		input: input.directories.test.contents
		path:  "test.txt"
	} & {
		contents: "local directory\n"
	}
}
