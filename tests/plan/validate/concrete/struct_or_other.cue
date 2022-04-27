package main

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"
)

#Test: {
	required: {
		field: string
	} | *null
	_op: core.#Nop & {
		input: required
	}
}

dagger.#Plan & {
	actions: test: #Test & {
	}
}
