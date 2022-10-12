package docs

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"

	"universe.dagger.io/alpine"
	"universe.dagger.io/bash"
	"universe.dagger.io/docker"
	"universe.dagger.io/docker/cli"
)

dagger.#Plan & {
	client: {
		// Locally, manual source of the .env or install https://direnv.net
		env: {
			GITHUB_SHA:                   string
			SSH_PRIVATE_KEY_DOCKER_SWARM: dagger.#Secret
		}
		filesystem: {
			"./": read: contents:              dagger.#FS
			"./merge.output": write: contents: actions.build.image.rootfs // Creates a build artifact for debug
		}
		network: "unix:///var/run/docker.sock": connect: dagger.#Socket // Docker daemon socket
	}

	actions: {
		params: image: {
			ref:      "registry.particubes.com/lua-docs"
			tag:      "latest"
			localTag: "test-particubes" // name of the image when being run locally
		}

		_dockerCLI: alpine.#Build & {
			packages: {
				bash: {}
				curl: {}
				"docker-cli": {}
				"openssh-client": {}
			}
		}

		#_verifyGithubSHA: bash.#Run & {
			input: _dockerCLI.output
			env: GITHUB_SHA: client.env.GITHUB_SHA
			always: true
			script: contents: #"""
				TRIMMED_URL="$(echo $URL | cut -d '/' -f 1)"
				curl --verbose --fail --connect-timeout 5 --location "$URL" >"$TRIMMED_URL.curl.out" 2>&1

				if ! grep "$GITHUB_SHA" "$TRIMMED_URL.curl.out"
				then
					echo "$GITHUB_SHA not present in the $TRIMMED_URL response:"
					cat "$TRIMMED_URL.curl.out"
					exit 1
				fi
				"""#
		}

		build: {
			luaDocs: docker.#Dockerfile & {
				source: client.filesystem."./lua-docs".read.contents
			}

			_addGithubSHA: core.#WriteFile & {
				input:    luaDocs.output.rootfs
				path:     "/www/github_sha.yml"
				contents: #"""
					keywords: ["particubes", "game", "mobile", "scripting", "cube", "voxel", "world", "docs"]
					title: "Github SHA"
					blocks:
					    - text: "\#(client.env.GITHUB_SHA)"
					"""#
			}
			image: docker.#Image & {
				rootfs: _addGithubSHA.output
				config: luaDocs.output.config
			}
		}

		clean: cli.#Run & {
			host:   client.network."unix:///var/run/docker.sock".connect
			always: true
			env: IMAGE_NAME: params.image.localTag
			command: {
				name: "sh"
				flags: "-c": #"""
					docker rm --force "$IMAGE_NAME"
					"""#
			}
		}

		test: {
			preLoad: clean

			load: cli.#Load & {
				image: build.image
				host:  client.network."unix:///var/run/docker.sock".connect
				tag:   params.image.localTag
				env: DEP: "\(preLoad.success)" // DEP created wth preLoad
			}

			run: cli.#Run & {
				host:   client.network."unix:///var/run/docker.sock".connect
				always: true
				env: {
					IMAGE_NAME: params.image.localTag
					PORTS:      "80:80"
					DEP:        "\(load.success)" // DEP created wth load
				}
				command: {
					name: "sh"
					flags: "-c": #"""
						docker run -d --rm --name "$IMAGE_NAME" -p "$PORTS" "$IMAGE_NAME"
						"""#
				}
			}

			verify: #_verifyGithubSHA & {
				env: {
					URL: "localhost/github_sha"
					DEP: "\(run.success)" // DEP created wth run
				}
			}

			postVerify: clean & {
				env: DEP: "\(verify.success)" // DEP created wth verify
			}
		}

		deploy: {
			publish: docker.#Push & {
				dest:  "\(params.image.ref):\(params.image.tag)"
				image: build.image
			}

			update: cli.#Run & {
				host:   "ssh://ubuntu@3.139.83.217"
				always: true
				ssh: key: client.env.SSH_PRIVATE_KEY_DOCKER_SWARM
				env: DEP: "\(publish.result)" // DEP created wth publish
				command: {
					name: "sh"
					flags: "-c": #"""
						docker service update --image registry.particubes.com/lua-docs:latest lua-docs
						"""#
				}
			}

			verify: #_verifyGithubSHA & {
				env: {
					URL: "https://docs.particubes.com/github_sha"
					DEP: "\(update.success)" // DEP created wth run
				}
			}
		}
	}
}
