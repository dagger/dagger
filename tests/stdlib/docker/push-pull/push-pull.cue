package docker

import (
	"dagger.io/dagger"
	"dagger.io/dagger/op"
	"dagger.io/alpine"
	"dagger.io/docker"
)

source: dagger.#Artifact

registry: {
	username: string
	secret:   dagger.#Secret
}

TestPushAndPull: {
	// Generate a random number
	random: {
		string
		#up: [
			op.#Load & {from: alpine.#Image},
			op.#Exec & {
				args: ["sh", "-c", "cat /dev/urandom | tr -dc 'a-z' | fold -w 10 | head -n 1 | tr -d '\n' > /rand"]
			},
			op.#Export & {
				source: "/rand"
			},
		]
	}

	ref: "daggerio/ci-test:\(random)"

	// Create image
	image: docker.#ImageFromDockerfile & {
		dockerfile: """
			 FROM alpine
			 COPY test.txt /test.txt
			"""
		context: source
	}

	// Login
	login: #up: [
		op.#DockerLogin & {
			registry
		},
	]

	// Push image
	push: docker.#Push & {
		"ref":  ref
		source: image
	}

	// Push image
	pull: docker.#Pull & {
		from: push.ref
	}

	//  Check the content
	verify: #up: [
		op.#Load & {from: alpine.#Image},
		op.#Exec & {
			always: true
			args: [
				"sh", "-c", """
					 grep -q "test" /src/test.txt
					""",
			]
			mount: "/src": from: pull
		},
	]
}
