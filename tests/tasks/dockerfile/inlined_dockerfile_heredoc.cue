package testing

import (
	"dagger.io/dagger"
)

dagger.#Plan & {
	inputs: directories: testdata: path: "./testdata"

	actions: {
		build: dagger.#Dockerfile & {
			source: inputs.directories.testdata.contents
			dockerfile: contents: """
				# syntax = docker/dockerfile:1.3
				FROM alpine:latest@sha256:ab00606a42621fb68f2ed6ad3c88be54397f981a7b70a79db3d1172b11c4367d
				RUN echo foobar > /output
				"""
		}

		verify: dagger.#Exec & {
			input: build.output
			args: ["sh", "-c", "test $(cat /output) = foobar"]
		}
	}
}

// TestDockerfilePath: #up: [
//  op.#DockerBuild & {
//   context:        TestData
//   dockerfilePath: "./dockerfilepath/Dockerfile.custom"
//  },
//  op.#Exec & {
//   args: ["sh", "-c", "test $(cat /test) = dockerfilePath"]
//  },
// ]

// TestBuildArgs: #up: [
//  op.#DockerBuild & {
//   dockerfile: """
//    FROM alpine:latest@sha256:ab00606a42621fb68f2ed6ad3c88be54397f981a7b70a79db3d1172b11c4367d
//    ARG TEST=foo
//    RUN test "${TEST}" = "bar"
//    """
//   buildArg: TEST: "bar"
//  },
// ]

// // FIXME: this doesn't test anything beside not crashing
// TestBuildLabels: #up: [
//  op.#DockerBuild & {
//   dockerfile: """
//    FROM alpine:latest@sha256:ab00606a42621fb68f2ed6ad3c88be54397f981a7b70a79db3d1172b11c4367d
//    """
//   label: FOO: "bar"
//  },
// ]

// // FIXME: this doesn't test anything beside not crashing
// TestBuildPlatform: #up: [
//  op.#DockerBuild & {
//   dockerfile: """
//    FROM alpine:latest@sha256:ab00606a42621fb68f2ed6ad3c88be54397f981a7b70a79db3d1172b11c4367d
//    """
//   platforms: ["linux/amd64"]
//  },
// ]

// TestImageMetadata: #up: [
//  op.#DockerBuild & {
//   dockerfile: """
//    FROM alpine:latest@sha256:ab00606a42621fb68f2ed6ad3c88be54397f981a7b70a79db3d1172b11c4367d
//    ENV CHECK foobar
//    ENV DOUBLECHECK test
//    """
//  },
//  op.#Exec & {
//   args: ["sh", "-c", #"""
//    env
//    test "$CHECK" = "foobar"
//    """#]
//  },
// ]

// // Make sure the metadata is carried over with a `Load`
// TestImageMetadataIndirect: #up: [
//  op.#Load & {
//   from: TestImageMetadata
//  },
//  op.#Exec & {
//   args: ["sh", "-c", #"""
//    env
//    test "$DOUBLECHECK" = "test"
//    """#]
//  },
// ]
