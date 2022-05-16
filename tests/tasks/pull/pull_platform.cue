package main

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"
)

dagger.#Plan & {
	// Alpine manifest list
	#image: "alpine:3.15.0@sha256:21a3deaa0d32a8057914f36584b5288d2e5ecc984380bc0118285c70fa8c9300"

	// Platforms lists
	#platforms: {
		"linux/amd64": "x86_64"
		"linux/arm64": "aarch64"
		"linux/s390x": "s390x"
	}

	actions: pull: {
		"linux/amd64": _
		"linux/arm64": _
		"linux/s390x": _

		[p=string]: {
			image: core.#Pull & {
				source:   #image
				platform: p
			}

			printArch: core.#Exec & {
				input:  image.output
				always: true
				args: ["/bin/sh", "-c", "uname -m | tr -d '\n' >> /arch.txt"]
			}

			verify: core.#ReadFile & {
				input: printArch.output
				path:  "/arch.txt"
			} & {
				contents: #platforms[p]
			}
		}
	}
}
