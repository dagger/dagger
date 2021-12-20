package main

import "alpha.dagger.io/europa/dagger/engine"

engine.#Plan & {
	actions: invalid: engine.#GitPull & {}
}
