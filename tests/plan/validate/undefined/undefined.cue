package main

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"
)

dagger.#Plan & {
	client: {}

	actions: test: {
		undefinedAction: core.#Nop & {
			input: actions.nonexistent
		}

		undefinedDef: core.#NonExistent & {
			input: dagger.#Scratch
		}

		filesystem: core.#Nop & {
			input: client.filesystem."/non/existent".read.contents
		}

		interpolation: core.#ReadFile & {
			input: dagger.#Scratch
			path:  "/wrong/\(core.#NonExistent)"
		}

		disjunction: string | *"default"
		disjunction: core.#NonExistent
	}
}
