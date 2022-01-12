package main

import "dagger.io/dagger/engine"

engine.#Plan & {
	actions: badref: engine.#GitPull & {
		remote: "https://github.com/blocklayerhq/acme-clothing.git"
		ref:    "lalalalal"
	}
}
