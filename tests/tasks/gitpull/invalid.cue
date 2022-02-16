package main

import (
	"dagger.io/dagger"
)

dagger.#Plan & {
	actions: invalid: dagger.#GitPull & {}
}
