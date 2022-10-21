package main

import (
	"dagger.io/dagger"
	"universe.dagger.io/docker"
)

dagger.#Plan & {
	actions: versions: {
		"8.0": _
		"5.7": _

		// This is a template
		// See https://cuelang.org/docs/tutorials/tour/types/templates/
		[tag=string]: {
			build: docker.#Build & {
				steps: [
					docker.#Pull & {
						source: "mysql:\(tag)"
					},
					docker.#Set & {
						config: cmd: [
							"--character-set-server=utf8mb4",
							"--collation-server=utf8mb4_unicode_ci",
						]
					},
				]
			}
			push: docker.#Push & {
				image: build.output
				dest:  "registry.example.com/mysql:\(tag)"
			}
		}
	}
}
