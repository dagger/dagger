package main

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"
)

dagger.#Plan & {
	client: {
		env: TMP_DIR: string // supports dynamic paths as well
		filesystem: {
			foo: write: {
				path:     env.TMP_DIR
				contents: actions.test.foo.output
			}
			bar: write: {
				path:     env.TMP_DIR
				contents: actions.test.bar.output
			}
		}
	}
	actions: test: {
		foo: core.#WriteFile & {
			input:    dagger.#Scratch
			path:     "/foo.txt"
			contents: "foo"
		}
		bar: core.#WriteFile & {
			input:    dagger.#Scratch
			path:     "/bar.txt"
			contents: "bar"
		}
	}
}
