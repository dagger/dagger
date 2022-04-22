package bats

import (
	"dagger.io/dagger"

	"universe.dagger.io/docker"
	"universe.dagger.io/bash"
)

#Bats: {
	// Source code
	source: dagger.#FS

	assets: [dagger.#FS]

	// shellcheck version
	version: *"1.6.0" | string

	docker.#Build & {
		steps: [
			docker.#Pull & {
				source: "bats/bats:\(version)"
			},

			docker.#Copy & {
				contents: source
				include: ["tests"]
				dest: "/src"
			},

			bash.#Run & {
				entrypoint: _
				workdir:    "/src/tests"
				script: contents: #"""
					apk add --no-cache yarn
					yarn add bats-support bats-assert
					"""#
			},

			bash.#Run & {
				entrypoint: _
				workdir:    "/src/tests"
				script: contents: #"""
					bats --jobs 4 --print-output-on-failure --verbose-run .
					"""#
			},
		]
	}
}
