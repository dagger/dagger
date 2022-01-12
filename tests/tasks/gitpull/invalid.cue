package main

import "dagger.io/dagger/engine"

engine.#Plan & {
	actions: invalid: engine.#GitPull & {}
}
