package main

import (
	"dagger.io/dagger/sdk/go/dagger"
	"universe.dagger.io/python"
)

dagger.#Plan & {
	client: _

	actions: test: python.#Run & {
		script: contents: "print('Platform: \(client.platform.os) / \(client.platform.arch)')"
		always: true
	}
}
