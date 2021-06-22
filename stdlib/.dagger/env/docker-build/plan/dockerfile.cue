package docker

import (
	"dagger.io/dagger"
	"dagger.io/dagger/op"
	"dagger.io/docker"
)

TestSourceBuild: dagger.#Artifact @dagger(input)

TestBuild: {
	image: docker.#Build & {
		source: TestSourceBuild
	}

	verify: #up: [
		op.#Load & {
			from: image
		},

		op.#Exec & {
			always: true
			args: [
				"sh", "-c", """
						grep -q "test" /test.txt
					""",
			]
		},
	]
}

TestSourceImageFromDockerfile: dagger.#Artifact @dagger(input)

TestImageFromDockerfile: {
	image: docker.#ImageFromDockerfile & {
		dockerfile: """
				FROM alpine
				COPY test.txt /test.txt
			"""
		context: TestSourceImageFromDockerfile
	}

	verify: #up: [
		op.#Load & {
			from: image
		},

		op.#Exec & {
			always: true
			args: [
				"sh", "-c", """
						grep -q "test" /test.txt
					""",
			]
		},
	]
}
