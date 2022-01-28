package test

import (
	"dagger.io/dagger"
	"dagger.io/dagger/engine"
	"universe.dagger.io/docker"
)

// This test verify that we can correctly build an image
// using docker.#Build with multiple steps executed during
// the building process
dagger.#Plan & {
	inputs: directories: testdata: path: "./testdata"

	actions: {
		image: docker.#Build & {
			steps: [
				docker.#Pull & {
					source: "golang:1.17-alpine"
				},
				docker.#Copy & {
					contents: inputs.directories.testdata.contents
					dest:     "/input"
				},
				docker.#Run & {
					script: """
							# FIXME remove that line when #1517 is merged
							export PATH=/go/bin:/usr/local/go/bin:$PATH
							go build -o hello ./main.go
							mv hello /bin
						"""
					workdir: "/input"
				},
				docker.#Run & {
					script: """
							hello >> /test.txt
						"""
				},
			]
		}

		verify: engine.#ReadFile & {
			input: image.output.rootfs
			path:  "/test.txt"
		} & {
			contents: "hello world"
		}
	}
}
