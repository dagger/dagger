package testing

import (
	"dagger.io/dagger/engine"
)

engine.#Plan & {
	inputs: directories: testdata: path: "./testdata"

	actions: {
		// FIXME: this doesn't test anything beside not crashing
		build: engine.#Dockerfile & {
			source: inputs.directories.testdata.contents
			dockerfile: contents: """
				FROM alpine:latest@sha256:ab00606a42621fb68f2ed6ad3c88be54397f981a7b70a79db3d1172b11c4367d
				ENV test foobar
				CMD /test-cmd
				"""
		} & {
			config: {
				env: test: "foobar"
				cmd: ["/bin/sh", "-c", "/test-cmd"]
			}
		}
	}
}
