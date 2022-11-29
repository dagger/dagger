package main

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"
)

dagger.#Plan & {
	actions: {
		pull: core.#Pull & {
			source: "alpine"
		}

		pull315: core.#Pull & {
			source: "alpine:3.15"
		}

		cat: core.#Exec & {
			input: pull.output
			args: ["cat", "/315/etc/alpine-release"]
			mounts: "315": {
				dest:     "/315"
				contents: pull315.output
			}
		}

		printenv: core.#Exec & {
			input: pull.output
			env: "DAGGER": "cloak"
			args: ["printenv", "DAGGER"]
		}
	}
}
