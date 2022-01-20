package main

import (
	"dagger.io/dagger/engine"
)

engine.#Plan & {
	// Alpine manifest list
	#image: "alpine:3.15.0@sha256:21a3deaa0d32a8057914f36584b5288d2e5ecc984380bc0118285c70fa8c9300"

	// Platforms lists
	#platforms: {
		"linux/amd64": "x86_64"
		"linux/arm64": "aarch64"
		"linux/s390x": "s390x"
	}

	actions: {
		for p, arch in #platforms {
			"test-\(p)": {
				image: engine.#Pull & {
					source:   #image
					platform: p
				}

				printArch: engine.#Exec & {
					input:  image.output
					always: true
					args: ["/bin/sh", "-c", "uname -m >> /arch.txt"]
				}

				test: engine.#ReadFile & {
					input: printArch.output
					path:  "/arch.txt"
				} & {
					contents: "\(arch)\n"
				}
			}
		}
	}
}
