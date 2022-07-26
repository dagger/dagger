package main

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"
	"universe.dagger.io/bash"
	"universe.dagger.io/docker"
)

dagger.#Plan & {
	actions: {
		build: {
			// core.#Source lets you access a file system tree (dagger.#FS)
			// using a path at "." or deeper (e.g. "./foo" or "./foo/bar") with
			// optional include/exclude of specific files/directories/globs
			checkoutCode: core.#Source & {
				path: "."
			}
			// Pulls from Docker Hub by default, but you can set registry/auth
			// Choosing an image with yarn/npm and bash already installed
			pull: docker.#Pull & {
				source: "node:lts"
			}
			// Copies content into input container's filesystem (at "/" by default)
			copy: docker.#Copy & {
				input:    pull.output
				contents: checkoutCode.output
			}
			// Runs a bash script in the input container
			install: bash.#Run & {
				input: copy.output
				script: contents: """
					yarn install
					# yarn install --frozen-lockfile
					yarn run build
					yarn run test
					"""
			}
		}
	}
}
