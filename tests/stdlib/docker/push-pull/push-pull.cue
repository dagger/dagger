package main

import (
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/dagger/op"
	"alpha.dagger.io/alpine"
	"alpha.dagger.io/docker"
	"alpha.dagger.io/random"
)

source: dagger.#Artifact

registry: {
	username: string
	secret:   string
}

TestPushAndPull: {
	tag: random.#String & {
		seed: ""
	}

	ref: "daggerio/ci-test:\(tag.out)"

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
