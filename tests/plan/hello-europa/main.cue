package main

import (
	"dagger.io/dagger"
	"alpha.dagger.io/os"
)

dagger.#Plan & {
	actions: {
		sayHello: os.#Container & {
			command: "echo Hello Europa! > /out.txt"
		}

		verify: "Hello Europa!\n" & (os.#File & {from: sayHello, path: "/out.txt"}).contents
	}
}
