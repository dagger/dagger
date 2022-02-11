package go

import (
	"dagger.io/dagger"
	"dagger.io/dagger/engine"
	"universe.dagger.io/go"
)

dagger.#Plan & {
	actions: tests: container: {
		_source: engine.#Scratch & {}

		simple: go.#Container & {
			source: _source
			args: ["version"]
		}
	}
}
