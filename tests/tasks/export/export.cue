package main

import (
	"dagger.io/dagger"
)

dagger.#Plan & {
	actions: test: {
		image: dagger.#Pull & {
			source: "alpine:3.15"
		}

		export: dagger.#Export & {
			input:  image.output
			config: image.config
			tag:    "example"
		}

		verify: dagger.#Exec & {
			input: image.output
			mounts: exported: {
				contents: export.output
				dest:     "/src"
			}
			args: ["tar", "tf", "/src/image.tar", "manifest.json"]
		}
	}
}
