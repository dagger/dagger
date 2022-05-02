package main

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"
)

dagger.#Plan & {
	client: env: TEST: string
	actions: test: {
		site:    client.env.TEST
		command: core.#Nop & {
			input: dagger.#Scratch
		}
	}
}
