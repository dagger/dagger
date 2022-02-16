package main

import (
)

dagger.#Plan & {
	platform: "linux/arm64"

	actions: {
		image: dagger.#Pull & {
			source: "alpine:3.15.0"
		}

		writeArch: dagger.#Exec & {
			input:  image.output
			always: true
			args: [
				"sh", "-c", #"""
					echo -n $(uname -m) >> /arch.txt
					"""#,
			]
		}

		verify: dagger.#ReadFile & {
			input: writeArch.output
			path:  "/arch.txt"
		} & {
			contents: "aarch64"
		}
	}
}
