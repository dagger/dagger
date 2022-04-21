package debian

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"

	"universe.dagger.io/docker"

	"universe.dagger.io/x/gabriel.gazola@poli.ufrj.br/debian"
)

dagger.#Plan & {
	actions: test: {
		// Test: customize debian version
		debianVersion: {
			build: debian.#Build & {
				// install an old version on purpose
				version: "buster@sha256:1b236b48c1ef66fa08535a5153266f4959bf58f948db3e68f7d678b651d8e33a"
			}

			verify: core.#Exec & {
				input: build.output.rootfs
				args: ["[[ $(uname -r) != '5.13.0-39-generic' ]]"]
			}
		}

		// Test: install packages
		packageInstall: {
			build: debian.#Build & {
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
