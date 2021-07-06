// Docker container operations
package docker

import (
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/dagger/op"
)

// Build a Docker image from source, using included Dockerfile
#Build: {
	source: dagger.#Artifact @dagger(input)

	#up: [
		op.#DockerBuild & {
			context: source
		},
	]

}

// Pull a docker container
#Pull: {
	// Remote ref (example: "index.docker.io/alpine:latest")
	from: string @dagger(input)

	#up: [
		op.#FetchContainer & {ref: from},
	]
}

// Push a docker image to a remote registry
#Push: {
	// Remote target (example: "index.docker.io/alpine:latest")
	target: string @dagger(input)

	// Image source
	source: dagger.#Artifact @dagger(input)

	// Registry auth
	auth?: {
		// Username
		username: string @dagger(input)

		// Password or secret
		secret: string @dagger(input)
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
	} @dagger(output)

	// Image digest
	digest: {
		string

		#up: [
			op.#Load & {from: push},

			op.#Export & {
				source: "/image_digest"
			},
		]
	} @dagger(output)
}

#Run: {
	// Connect to a remote SSH server
	ssh: {
		// ssh host
		host: string @dagger(input)

		// ssh user
		user: string @dagger(input)

		// ssh port
		port: *22 | int @dagger(input)

		// private key
		key: dagger.#Secret @dagger(input)

		// fingerprint
		fingerprint?: string @dagger(input)

		// ssh key passphrase
		keyPassphrase?: dagger.#Secret @dagger(input)
	}

	// Image reference (e.g: nginx:alpine)
	ref: string @dagger(input)

	// Container name
	name: string | *null @dagger(input)

	// Build and directly run the source
	build: {
		// Source directory
		source: dagger.#Artifact @dagger(input)

		// Path to dockerfile
		path: *"Dockerfile" | string @dagger(input)
	} | *null

	// Image registry
	registry?: {
		target:   string
		username: string
		secret:   dagger.#Secret
	} @dagger(input)

	container: #Command & {
		"ssh": ssh
		command: #"""
			# Run detach container
			OPTS=""

			if [ ! -z "$CONTAINER_NAME" ]; then
				OPTS="$OPTS --name $CONTAINER_NAME"
			fi

			if [ -d /source ]; then
				docker build -t "$IMAGE_REF" -f "/source/$DOCKERFILE_PATH"  /source
			fi

			mkdir -p /outputs
			docker container run -d $OPTS "$IMAGE_REF" | tr -d "\n" > /outputs/container_id
			"""#
		env: {
			IMAGE_REF: ref
			if name != null {
				CONTAINER_NAME: name
				if build != null {
					DOCKERFILE_PATH: build.path
				}
			}
		}
		if build != null {
			mount: "/source": from: build.source
		}
	}

	// Running container id
	id: {
		string

		#up: [
			op.#Load & {from: container},

			op.#Export & {
				source: "/outputs/container_id"
			},
		]
	} @dagger(output)
}

// Build a Docker image from the provided Dockerfile contents
// FIXME: incorporate into #Build
#ImageFromDockerfile: {
	// Dockerfile passed as a string
	dockerfile: string @dagger(input)

	// Build context
	context: dagger.#Artifact @dagger(input)

	#up: [
		op.#DockerBuild & {
			"context":    context
			"dockerfile": dockerfile
		},
	]
}
