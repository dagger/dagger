package testing

import (
	"dagger.io/dagger/engine"
)

engine.#Plan & {
	inputs: directories: testdata: path: "./testdata"

	actions: build: engine.#Dockerfile & {
		source: inputs.directories.testdata.contents
		contents: """
			FROM alpine:latest@sha256:ab00606a42621fb68f2ed6ad3c88be54397f981a7b70a79db3d1172b11c4367d
			ARG TEST=foo
			RUN test "${TEST}" = "bar"
			"""
		buildArg: TEST: "bar"
	}
}
