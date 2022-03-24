package docker

import (
	"dagger.io/dagger"

	"universe.dagger.io/alpine"
	"universe.dagger.io/bash"
	"universe.dagger.io/docker"
)

dagger.#Plan & {
	client: filesystem: "/var/run/docker.sock": read: contents: dagger.#Service

	actions: test: load: {
		_cli: alpine.#Build & {
			packages: {
				bash: {}
				"docker-cli": {}
			}
		}

		_image: docker.#Run & {
			input: _cli.output
			command: {
				name: "touch"
				args: ["/foo.bar"]
			}
		}

		load: docker.#Load & {
			image: _image.output
			host:  client.filesystem."/var/run/docker.sock".read.contents
			tag:   "dagger:load"
		}

		verify: bash.#Run & {
			input: _cli.output
			mounts: docker: {
				contents: client.filesystem."/var/run/docker.sock".read.contents
				dest:     "/var/run/docker.sock"
			}
			env: {
				IMAGE_NAME: load.tag
				IMAGE_ID:   load.imageID
				// FIXME: without this forced dependency, load.command might not run
				DEP: "\(load.success)"
			}
			script: contents: #"""
				test "$(docker image inspect $IMAGE_NAME -f '{{.Id}}')" = "$IMAGE_ID"
				docker run --rm $IMAGE_NAME stat /foo.bar
				"""#
		}
	}

	// FIXME: test remote connections with `docker:dind`
	// image when we have long running tasks
}
