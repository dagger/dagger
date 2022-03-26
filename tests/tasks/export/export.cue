package main

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"
)

dagger.#Plan & {
	actions: test: {
		image: core.#Pull & {
			source: "alpine:3.15"
		}

		export: core.#Export & {
			input:  image.output
			config: image.config
			tag:    "example"
		}

		verify: core.#Exec & {
			input: image.output
			mounts: exported: {
				contents: export.output
				dest:     "/src"
			}
			args: ["tar", "tf", "/src/image.tar", "manifest.json"]
		}
	}
}
