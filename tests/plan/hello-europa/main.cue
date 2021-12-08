package main

import (
	"alpha.dagger.io/dagger/engine"
	"alpha.dagger.io/os"
)

engine.#Plan & {
	actions: sayHello: os.#Container & {
		command: "echo Hello Europa!"
	}
}
