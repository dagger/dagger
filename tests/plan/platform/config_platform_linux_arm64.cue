package main

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"
)

dagger.#Plan & {
	platform: "linux/arm64"

	actions: {
		image: core.#Pull & {
			source: "alpine:3.15.0"
		}

		writeArch: core.#Exec & {
			input:  image.output
			always: true
			args: [
				"sh", "-c", #"""
					echo -n $(uname -m) >> /arch.txt
					"""#,
			]
		}

		verify: core.#ReadFile & {
			input: writeArch.output
			path:  "/arch.txt"
		} & {
			contents: "aarch64"
		}
	}
}
