package main

import (
	"alpha.dagger.io/dagger/engine"
	"alpha.dagger.io/os"
)

engine.#Plan & {
	actions: {
		sayHello: os.#Container & {
			command: "echo Hello Europa! > /out.txt"
		}

		verify: "Hello Europa!\n" & (os.#File & {from: sayHello, path: "/out.txt"}).contents
	}
}
