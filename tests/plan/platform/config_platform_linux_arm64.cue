package main

import (
	"dagger.io/dagger/engine"
)

engine.#Plan & {
	platform: "linux/arm64"

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
			contents: "aarch64"
		}
	}
}
