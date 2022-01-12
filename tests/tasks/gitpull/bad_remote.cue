package main

import "dagger.io/dagger/engine"

engine.#Plan & {
	actions: badremote: engine.#GitPull & {
		remote: "https://github.com/blocklayerhq/lalalala.git"
		ref:    "master"
	}
}
