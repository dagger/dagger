package testing

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"
)

dagger.#Plan & {
	client: filesystem: testdata: read: contents: dagger.#FS

	actions: {
		// FIXME: this doesn't test anything beside not crashing
		build: core.#Dockerfile & {
			source: client.filesystem.testdata.read.contents
			dockerfile: contents: """
				FROM alpine:latest@sha256:ab00606a42621fb68f2ed6ad3c88be54397f981a7b70a79db3d1172b11c4367d
				"""
			platforms: ["linux/amd64"]
		}
	}
}
