package test

import (
	"dagger.io/dagger"
	"dagger.io/llb"
)

// Set to `--input-dir=./tests/dockerbuild/testdata`
TestData: dagger.#Artifact

TestInlinedDockerfile: #up: [
	llb.#DockerBuild & {
		dockerfile: """
			FROM alpine:latest@sha256:ab00606a42621fb68f2ed6ad3c88be54397f981a7b70a79db3d1172b11c4367d
			RUN echo hello world
			"""
	},
]

TestOpChaining: #up: [
	llb.#DockerBuild & {
		dockerfile: """
			FROM alpine:latest@sha256:ab00606a42621fb68f2ed6ad3c88be54397f981a7b70a79db3d1172b11c4367d
			RUN echo foobar > /output
			"""
	},
	llb.#Exec & {
		args: ["sh", "-c", "test $(cat /output) = foobar"]
	},
]

TestBuildContext: #up: [
	llb.#DockerBuild & {
		context: TestData
	},
	llb.#Exec & {
		args: ["sh", "-c", "test $(cat /dir/foo) = foobar"]
	},
]

TestBuildContextAndDockerfile: #up: [
	llb.#DockerBuild & {
		context: TestData
		dockerfile: """
			FROM alpine:latest@sha256:ab00606a42621fb68f2ed6ad3c88be54397f981a7b70a79db3d1172b11c4367d
			COPY foo /override
			"""
	},
	llb.#Exec & {
		args: ["sh", "-c", "test $(cat /override) = foobar"]
	},
]

TestDockerfilePath: #up: [
	llb.#DockerBuild & {
		context:        TestData
		dockerfilePath: "./dockerfilepath/Dockerfile.custom"
	},
	llb.#Exec & {
		args: ["sh", "-c", "test $(cat /test) = dockerfilePath"]
	},
]

TestBuildArgs: #up: [
	llb.#DockerBuild & {
		dockerfile: """
			FROM alpine:latest@sha256:ab00606a42621fb68f2ed6ad3c88be54397f981a7b70a79db3d1172b11c4367d
			ARG TEST=foo
			RUN test "${TEST}" = "bar"
			"""
		buildArg: TEST: "bar"
	},
]

// FIXME: this doesn't test anything beside not crashing
TestBuildLabels: #up: [
	llb.#DockerBuild & {
		dockerfile: """
			FROM alpine:latest@sha256:ab00606a42621fb68f2ed6ad3c88be54397f981a7b70a79db3d1172b11c4367d
			"""
		label: FOO: "bar"
	},
]

// FIXME: this doesn't test anything beside not crashing
TestBuildPlatform: #up: [
	llb.#DockerBuild & {
		dockerfile: """
			FROM alpine:latest@sha256:ab00606a42621fb68f2ed6ad3c88be54397f981a7b70a79db3d1172b11c4367d
			"""
		platforms: ["linux/amd64"]
	},
]
