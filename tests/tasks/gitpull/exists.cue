package main

import "alpha.dagger.io/europa/dagger/engine"

engine.#Plan & {
	actions: gitPull: engine.#GitPull & {
		remote: "https://github.com/blocklayerhq/acme-clothing.git"
		ref:    "master"
	}
}
