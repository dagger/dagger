package test

import (
	"dagger.io/dagger"

	"universe.dagger.io/bats"
)

dagger.#Plan & {
	inputs: directories: testfile: path: "./testfile"

	actions: test: bats.#Bats & {
		source: inputs.directories.testfile.contents
	}
}
