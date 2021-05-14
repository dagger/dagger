package docker

import (
	"dagger.io/dagger"
	"dagger.io/dagger/op"
	"dagger.io/docker"
)

// Build a Docker image from source, using included Dockerfile
source: dagger.#Artifact

TestBuild: {
	image: docker.#Build & {
		"source": source
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
