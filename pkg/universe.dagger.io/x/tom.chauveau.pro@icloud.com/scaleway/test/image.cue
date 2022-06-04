package scaleway

import (
	"dagger.io/dagger"

	"universe.dagger.io/docker"
	"universe.dagger.io/x/tom.chauveau.pro@icloud.com/scaleway"
)

dagger.#Plan & {
	actions: test: image: {
		simple: {
			_image: scaleway.#Image

			verify: docker.#Run & {
				input: _image.output
				command: {
					name: "-c"
					args: ["apk add jq && /scw version -o json | jq .version  >> /version.txt"]
				}
				entrypoint: ["/bin/sh"]
				export: files: "/version.txt": string & =~"2.4.0"
			}
		}

		custom: {
			_image: scaleway.#Image & {
				version: "2"
			}

			verify: docker.#Run & {
				input: _image.output
				command: {
					name: "-c"
					args: ["apk add jq && /scw version -o json | jq .version  >> /version.txt"]
				}
				entrypoint: ["/bin/sh"]
				export: files: "/version.txt": string & =~"2.0.0"
			}
		}
	}
}
