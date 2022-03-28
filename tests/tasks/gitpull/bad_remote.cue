package main

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"
)

dagger.#Plan & {
	actions: badremote: core.#GitPull & {
		remote: "https://github.com/blocklayerhq/lalalala.git"
		ref:    "master"
	}
}
