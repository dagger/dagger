package bats

import (
	"dagger.io/dagger"

	"universe.dagger.io/docker"
	"universe.dagger.io/bash"
)

#Bats: {
	// Source code
	source: dagger.#FS

	initScript: string | *null

	// Environment variables to pass to bats
	env: [string]: string

	// Bats version
	version: *"1.6.0" | string

	// Mount points for the bats container
	mounts: [name=string]: _

	docker.#Build & {
		_packages: ["yarn", "git", "docker", "curl"]
		_batsMods: ["bats-support", "bats-assert"]

		steps: [
			docker.#Pull & {
				source: "bats/bats:\(version)"
			},

			// Symlink bash so we can `bash.#Run` entrypoint can work
			docker.#Run & {
				entrypoint: []
				command: {
					name: "ln"
					args: ["-sf", "/usr/local/bin/bash", "/bin/bash"]
				}
			},

			docker.#Copy & {
				contents: source
				dest:     "/src"
			},

			for pkg in _packages {
				docker.#Run & {
					entrypoint: []
					command: {
						name: "apk"
						args: ["add", "--no-cache", pkg]
					}
				}
			},

			for mod in _batsMods {
				docker.#Run & {
					entrypoint: []
					workdir: "/src"
					command: {
						name: "yarn"
						args: ["add", mod]
					}
				}
			},

			if initScript != null {
				bash.#Run & {
					"env":   env
					workdir: "/src"
					script: contents: initScript
				}
			},

			bash.#Run & {
				"env":    env
				"mounts": mounts
				workdir:  "/src"
				script: contents: """
					bats --jobs 4 --print-output-on-failure --verbose-run .
					"""
			},
		]
	}
}
