package main

import (
	"dagger.io/dagger"
)

dagger.#Plan & {
	actions: badref: dagger.#GitPull & {
		remote: "https://github.com/blocklayerhq/acme-clothing.git"
		ref:    "lalalalal"
	}
}
