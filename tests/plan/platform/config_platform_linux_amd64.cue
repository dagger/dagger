package main

import (
	"alpha.dagger.io/europa/dagger/engine"
)

engine.#Plan & {
	platform: "linux/amd64"

	actions: {
		image: engine.#Pull & {
			source: "alpine:3.15.0"
		}

		writeArch: engine.#Exec & {
			input:  image.output
			always: true
			args: [
				"sh", "-c", #"""
					echo -n $(uname -m) >> /arch.txt
					"""#,
			]
		}

		verify: engine.#ReadFile & {
			input: writeArch.output
			path:  "/arch.txt"
		} & {
			contents: "x86_64"
		}
	}
}
