package main

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"
)

#Test: {
	required: string | int
	_op:      core.#Pull & {
		source: required
	}
}

dagger.#Plan & {
	actions: test: #Test & {}
}
