package main

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"
)

dagger.#Plan & {
	actions: badref: core.#GitPull & {
		remote: "https://github.com/blocklayerhq/acme-clothing.git"
		ref:    "lalalalal"
	}
}
