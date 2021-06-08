package main

import (
	"dagger.io/dagger"
	"dagger.io/dagger/op"
	"dagger.io/alpine"
	"dagger.io/docker"
)

source: dagger.#Artifact

registry: {
	username: string
	secret:   string
}

TestPushAndPull: {
	random: #Random & {}

	ref: "daggerio/ci-test:\(random.out)"

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
