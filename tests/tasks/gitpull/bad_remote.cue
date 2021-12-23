package main

import "alpha.dagger.io/europa/dagger/engine"

engine.#Plan & {
	actions: badremote: engine.#GitPull & {
		remote: "https://github.com/blocklayerhq/lalalala.git"
		ref:    "master"
	}
}
