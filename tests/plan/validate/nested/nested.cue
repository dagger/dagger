package main

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"
)

dagger.#Plan & {
	actions: test: {
		shallow: core.#ReadFile & {
			_write: core.#WriteFile & {
				input:    dagger.#Scratch
				path:     "/hello.txt"
				contents: "hello world"
			}
			input: _write.output
			path:  "/hello.txt"
		}
		deep: core.#ReadFile & {
			_op: write: core.#WriteFile & {
				input:    dagger.#Scratch
				path:     "/hello.txt"
				contents: "hello world"
			}
			input: _op.write.output
			path:  "/hello.txt"
		}
		nested: core.#ReadFile & {
			_copy: core.#Copy & {
				_write: core.#WriteFile & {
					input:    dagger.#Scratch
					path:     "/hello.txt"
					contents: "hello world"
				}
				input:    dagger.#Scratch
				contents: _write.output
			}
			input: _copy.output
			path:  "/hello.txt"
		}
	}
}
