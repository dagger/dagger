package main

import (
	"dagger.io/dagger"
)

dagger.#Plan & {
	actions: badremote: dagger.#GitPull & {
		remote: "https://github.com/blocklayerhq/lalalala.git"
		ref:    "master"
	}
}
