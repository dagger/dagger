package alpine

import (
	"dagger.io/dagger"

	"universe.dagger.io/alpine"
	"universe.dagger.io/docker"
)

dagger.#Plan & {
	actions: test: {
		// Test: customize alpine version
		alpineVersion: {
			build: alpine.#Build & {
				// install an old version on purpose
				version: "3.10.9"
			}

			verify: dagger.#Readfile & {
				input:    build.output.rootfs
				path:     "/etc/alpine-release"
				contents: "3.10.9\n"
			}
		}

		// Test: install packages
		packageInstall: {
			build: alpine.#Build & {
				packages: {
					jq: {}
					curl: {}
				}
			}

			check: docker.#Run & {
				input: build.output
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
}
