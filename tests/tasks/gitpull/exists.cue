package main

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"
)

dagger.#Plan & {
	actions: gitPull: core.#GitPull & {
		remote: "https://github.com/blocklayerhq/acme-clothing.git"
		ref:    "master"
	}
}
