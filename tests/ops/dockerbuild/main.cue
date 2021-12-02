package testing

import (
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/dagger/op"
)

// Set to `--input-dir=./tests/dockerbuild/testdata`
TestData: dagger.#Artifact

TestInlinedDockerfile: #up: [
	op.#DockerBuild & {
		dockerfile: """
			FROM alpine:latest@sha256:ab00606a42621fb68f2ed6ad3c88be54397f981a7b70a79db3d1172b11c4367d
			RUN echo hello world
			"""
	},
]

TestOpChaining: #up: [
	op.#DockerBuild & {
		dockerfile: """
			FROM alpine:latest@sha256:ab00606a42621fb68f2ed6ad3c88be54397f981a7b70a79db3d1172b11c4367d
			RUN echo foobar > /output
			"""
	},
	op.#Exec & {
		args: ["sh", "-c", "test $(cat /output) = foobar"]
	},
]

TestBuildContext: #up: [
	op.#DockerBuild & {
		context: TestData
	},
	op.#Exec & {
		args: ["sh", "-c", "test $(cat /dir/foo) = foobar"]
	},
]

TestBuildContextAndDockerfile: #up: [
	op.#DockerBuild & {
		context: TestData
		dockerfile: """
			FROM alpine:latest@sha256:ab00606a42621fb68f2ed6ad3c88be54397f981a7b70a79db3d1172b11c4367d
			COPY foo /override
			"""
	},
	op.#Exec & {
		args: ["sh", "-c", "test $(cat /override) = foobar"]
	},
]

TestDockerfilePath: #up: [
	op.#DockerBuild & {
		context:        TestData
		dockerfilePath: "./dockerfilepath/Dockerfile.custom"
	},
	op.#Exec & {
		args: ["sh", "-c", "test $(cat /test) = dockerfilePath"]
	},
]

TestBuildArgs: #up: [
	op.#DockerBuild & {
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
	op.#DockerBuild & {
		dockerfile: """
			FROM alpine:latest@sha256:ab00606a42621fb68f2ed6ad3c88be54397f981a7b70a79db3d1172b11c4367d
			"""
		label: FOO: "bar"
	},
]

// Verify that we can build an image with a targeted platform diffrent than host's one
TestBuildPlatform: {
	amd64: #up: [
		op.#DockerBuild & {
			dockerfile: """
				FROM alpine:latest@sha256:ab00606a42621fb68f2ed6ad3c88be54397f981a7b70a79db3d1172b11c4367d

				RUN echo $(uname -a) > /platform.txt
				"""
			platform: "linux/amd64"
		},

		op.#Exec & {
			always: true
			args: ["/bin/sh", "-c", "cat /platform.txt | grep 'x86_64'"]
		},
	]

	arm64: #up: [
		op.#DockerBuild & {
			dockerfile: """
				FROM alpine:latest@sha256:ab00606a42621fb68f2ed6ad3c88be54397f981a7b70a79db3d1172b11c4367d

				RUN echo $(uname -a) > /platform.txt
				"""
			platform: "linux/arm64"
		},

		op.#Exec & {
			always: true
			args: ["/bin/sh", "-c", "cat /platform.txt | grep 'aarch64'"]
		},
	]

	s390x: #up: [
		op.#DockerBuild & {
			dockerfile: """
				FROM alpine:latest@sha256:ab00606a42621fb68f2ed6ad3c88be54397f981a7b70a79db3d1172b11c4367d

				RUN echo $(uname -a) > /platform.txt
				"""
			platform: "linux/s390x"
		},

		op.#Exec & {
			always: true
			args: ["/bin/sh", "-c", "cat /platform.txt | grep 's390x'"]
		},
	]
}

TestImageMetadata: #up: [
	op.#DockerBuild & {
		dockerfile: """
			FROM alpine:latest@sha256:ab00606a42621fb68f2ed6ad3c88be54397f981a7b70a79db3d1172b11c4367d
			ENV CHECK foobar
			ENV DOUBLECHECK test
			"""
	},
	op.#Exec & {
		args: ["sh", "-c", #"""
			env
			test "$CHECK" = "foobar"
			"""#]
	},
]

// Make sure the metadata is carried over with a `Load`
TestImageMetadataIndirect: #up: [
	op.#Load & {
		from: TestImageMetadata
	},
	op.#Exec & {
		args: ["sh", "-c", #"""
			env
			test "$DOUBLECHECK" = "test"
			"""#]
	},
]
