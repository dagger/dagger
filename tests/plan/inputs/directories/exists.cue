package main

import (
	"alpha.dagger.io/europa/dagger"
)

dagger.#Plan & {
	input: directories: test: path: "./plan/inputs/directories"
	actions: verify: input.directories.test.contents
}
