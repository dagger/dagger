package docker

import (
	"dagger.io/dagger"
	"dagger.io/dagger/op"
	"dagger.io/docker"
)

source: dagger.#Artifact

TestImageFromDockerfile: {
	image: docker.#ImageFromDockerfile & {
		dockerfile: """
				FROM alpine
				COPY test.txt /test.txt
			"""
		context: source
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
