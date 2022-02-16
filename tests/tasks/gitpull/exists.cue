package main

import (
	"dagger.io/dagger"
)

dagger.#Plan & {
	actions: gitPull: dagger.#GitPull & {
		remote: "https://github.com/blocklayerhq/acme-clothing.git"
		ref:    "master"
	}
}
