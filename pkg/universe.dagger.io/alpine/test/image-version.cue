package test

import (
	"dagger.io/dagger"
	"dagger.io/dagger/engine"
	"universe.dagger.io/alpine"
)

dagger.#Plan & {
	actions: {
		build: alpine.#Build & {
			// install an old version on purpose
			version: "3.10.9"
		}

		check: engine.#Readfile & {
			input:    build.output.rootfs
			path:     "/etc/alpine-release"
			contents: "3.10.9\n"
		}
	}
}
