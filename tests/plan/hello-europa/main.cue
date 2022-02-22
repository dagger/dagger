package main

import (
	"dagger.io/dagger"
	// "alpha.dagger.io/os"
)

dagger.#Plan & {
	actions: {
		// TODO/FIXME: Use Europa constructs for this
		// sayHello: os.#Container & {
		//  command: "echo Hello Europa! > /out.txt"
		// }

		// verify: "Hello Europa!\n" & (os.#File & {from: sayHello, path: "/out.txt"}).contents
	}
}
