package test

import (
	"dagger.io/dagger"
	"universe.dagger.io/alpine"
	"universe.dagger.io/docker"
)

dagger.#Plan & {
	actions: {
		build: alpine.#Build & {
			packages: {
				jq: {}
				curl: {}
			}
		}

		check: docker.#Run & {
			image: build.output
			command: {
				name: "sh"
				flags: "-c": """
					jq --version > /jq-version.txt
					curl --version > /curl-version.txt
					"""
			}

			export: files: {
				"/jq-version.txt": contents:   =~"^jq"
				"/curl-version.txt": contents: =~"^curl"
			}
		}
	}
}
