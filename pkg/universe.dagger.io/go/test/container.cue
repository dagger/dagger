package go

import (
	"dagger.io/dagger"
	"universe.dagger.io/go"
)

dagger.#Plan & {
	actions: tests: container: {
		_source: dagger.#Scratch & {}

		simple: go.#Container & {
			source: _source
			command: args: ["version"]
		}
	}
}
