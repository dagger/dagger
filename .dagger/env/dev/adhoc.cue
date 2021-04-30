package main

import (
	"dagger.io/dagger/op"
)

// Reproduce inline issue.
// See https://github.com/dagger/dagger/issues/395
test: adhoc: repro395: {
	good: {
		// This field is correctly computed because its intermediary pipeline is not inlined.
		hello: sayHello.message

		// Intermediary pipeline cannot be inlined: it must be visible in a field
		sayHello: {
			message: {
				string
				#up: [
					op.#FetchContainer & { ref: "alpine" },
					op.#Exec & {
						args: ["sh", "-c", "echo hello > /message"]
					},
					op.#Export & { source: "/message", format: "string" },
				]
			}
	  	}
	}
	bad: {
		// This field is NOT correctly computed because its intermediary pipeline is inlined.
		hello: {
			message: {
				string
				#up: [
					op.#FetchContainer & { ref: "alpine" },
					op.#Exec & {
						args: ["sh", "-c", "echo hello > /message"]
					},
					op.#Export & { source: "/message", format: "string" },
				]
			}
	  	}.message

	}
}
