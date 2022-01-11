// Docker container operations
package docker

import (
	"strings"
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/dagger/op"
)

// Build a Docker image from source
#Build: {
	// Build context
	source: dagger.#Input & {dagger.#Artifact}

	// Dockerfile passed as a string
	dockerfile: dagger.#Input & {*null | string}

	args?: [string]: string | dagger.#Secret

	#up: [
		op.#DockerBuild & {
			context: source
			if dockerfile != null {
				"dockerfile": dockerfile
			}
			if args != _|_ {
				buildArg: args
			}
		},
	]

}

// Pull a docker container
#Pull: {
	// Remote ref (example: "index.docker.io/alpine:latest")
	from: dagger.#Input & {string}

	#up: [
		op.#FetchContainer & {ref: from},
	]
}

// Push a docker image to a remote registry
#Push: {
	// Remote target (example: "index.docker.io/alpine:latest")
	target: dagger.#Input & {string}

	// Image source
	source: dagger.#Input & {dagger.#Artifact}

	// Registry auth
	auth?: {
		// Username
		username: dagger.#Input & {string}

		// Password or secret
		secret: dagger.#Input & {dagger.#Secret | string}
	}

	push: #up: [
		op.#Load & {from: source},

		if auth != _|_ {
			op.#DockerLogin & {
				"target": target
				username: auth.username
				secret:   auth.secret
			}
		},

		op.#PushContainer & {ref: target},

		op.#Subdir & {dir: "/dagger"},
	]

	// Image ref
	ref: {
		string

		#up: [
			op.#Load & {from: push},

			op.#Export & {
				source: "/image_ref"
			},
		]
	} & dagger.#Output

	// Image digest
	digest: {
		string

		#up: [
			op.#Load & {from: push},

			op.#Export & {
				source: "/image_digest"
			},
		]
	} & dagger.#Output
}

// Load a docker image into a docker engine
#Load: {
	// Connect to a remote SSH server
	ssh?: {
		// ssh host
		host: dagger.#Input & {string}

		// ssh user
		user: dagger.#Input & {string}

		// ssh port
		port: dagger.#Input & {*22 | int}

		// private key
		key: dagger.#Input & {dagger.#Secret}

		// fingerprint
		fingerprint?: dagger.#Input & {string}

		// ssh key passphrase
		keyPassphrase?: dagger.#Input & {dagger.#Secret}
	}

	// Connect via DOCKER_HOST, supports tcp://
	// TODO: Consider refactoring to support ssh:// & even file://
	host?: string @dagger(input)

	// Mount local docker socket
	socket?: dagger.#Stream & dagger.#Input

	// Name and optionally a tag in the 'name:tag' format
	tag: dagger.#Input & {string}

	// Image source
	source: dagger.#Input & {dagger.#Artifact}

	save: #up: [
		op.#Load & {from: source},

		op.#SaveImage & {
			"tag": tag
			dest:  "/image.tar"
		},
	]

	load: #Command & {
		if ssh != _|_ {
			"ssh": ssh
		}
		if host != _|_ && ssh == _|_ {
			"host": host
		}
		if socket != _|_ {
			"socket": socket
		}

		copy: "/src": from: save

		command: "docker load -i /src/image.tar"
	}

	// Image ID
	id: {
		string

		#up: [
			// HACK: force a dependency with `load`
			op.#Load & {from: load},

			op.#Load & {from: save},

			op.#Export & {
				source: "/dagger/image_id"
			},
		]
	} & dagger.#Output
}

#Run: {
	// Connect to a remote SSH server
	ssh?: {
		// ssh host
		host: dagger.#Input & {string}

		// ssh user
		user: dagger.#Input & {string}

		// ssh port
		port: dagger.#Input & {*22 | int}

		// private key
		key: dagger.#Input & {dagger.#Secret}

		// fingerprint
		fingerprint?: dagger.#Input & {string}

		// ssh key passphrase
		keyPassphrase?: dagger.#Input & {dagger.#Secret}
	}

	// Connect via DOCKER_HOST, supports tcp://
	// TODO: Consider refactoring to support ssh:// & even file://
	host?: string @dagger(input)

	// Mount local docker socket
	socket?: dagger.#Stream & dagger.#Input

	// Image reference (e.g: nginx:alpine)
	ref: dagger.#Input & {string}

	// Container name
	name?: dagger.#Input & {string}

	// Recreate container?
	recreate: dagger.#Input & {bool | *true}

	// Image registry
	registry?: {
		target:   string
		username: string
		secret:   dagger.#Secret
	} & dagger.#Input

	// local ports
	ports?: [...string]

	#command: #"""
		# Run detach container
		OPTS=""

		if [ ! -z "$CONTAINER_NAME" ]; then
			OPTS="$OPTS --name $CONTAINER_NAME"
			docker inspect "$CONTAINER_NAME" >/dev/null && {
				# Container already exists
				if [ ! -z "$CONTAINER_RECREATE" ]; then
					echo "Replacing container $CONTAINER_NAME"
					docker stop "$CONTAINER_NAME"
					docker rm "$CONTAINER_NAME"
				else
					echo "$CONTAINER_NAME already exists"
					exit 0
				fi
			}
		fi

		if [ ! -z "$CONTAINER_PORTS" ]; then
			OPTS="$OPTS -p $CONTAINER_PORTS"
		fi

		docker container run -d $OPTS "$IMAGE_REF"
		"""#

	run: #Command & {
		if ssh != _|_ {
			"ssh": ssh
		}
		if host != _|_ && ssh == _|_ {
			"host": host
		}
		if socket != _|_ {
			"socket": socket
		}

		command: #command
		env: {
			IMAGE_REF: ref
			if name != _|_ {
				CONTAINER_NAME: name
			}

			if recreate {
				CONTAINER_RECREATE: "true"
			}

			if ports != _|_ {
				CONTAINER_PORTS: strings.Join(ports, " -p ")
			}
		}
	}
}
