package docker

import (
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/dagger/op"
)

TestSourceBuild: dagger.#Artifact @dagger(input)

TestBuild: {
	image: #Image & {
		source: TestSourceBuild
	}

	test: #up: [
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
	image: #ImageFromDockerfile & {
		dockerfile: """
				FROM alpine
				COPY test.txt /test.txt
			"""
		context: TestSourceImageFromDockerfile
	}

	test: #up: [
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
