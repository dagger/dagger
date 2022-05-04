package main

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"
)

dagger.#Plan & {
	client: env: USER: string
	actions: test: {
		site:    string | *client.env.USER
		command: core.#Nop & {
			input: dagger.#Scratch
		}
	}
}
