package main

import "dagger.io/dagger"

dagger.#Plan & {
	outputs: files: test: dest: "./test"
}
