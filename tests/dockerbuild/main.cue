package test

import "dagger.io/dagger"

// Set to `--input-dir=./tests/dockerbuild/testdata`
TestData: dagger.#Dir

TestInlinedDockerfile: #dagger: compute: [
	dagger.#DockerBuild & {
		dockerfile: """
			FROM alpine:latest@sha256:ab00606a42621fb68f2ed6ad3c88be54397f981a7b70a79db3d1172b11c4367d
			RUN echo hello world
			"""
	},
]

TestOpChaining: #dagger: compute: [
	dagger.#DockerBuild & {
		dockerfile: """
			FROM alpine:latest@sha256:ab00606a42621fb68f2ed6ad3c88be54397f981a7b70a79db3d1172b11c4367d
			RUN echo foobar > /output
			"""
	},
	dagger.#Exec & {
		args: ["sh", "-c", "test $(cat /output) = foobar"]
	}
]

TestBuildContext: #dagger: compute: [
	dagger.#DockerBuild & {
		context: TestData
	},
	dagger.#Exec & {
		args: ["sh", "-c", "test $(cat /dir/foo) = foobar"]
	}
]

TestBuildContextAndDockerfile: #dagger: compute: [
	dagger.#DockerBuild & {
		context: TestData
		dockerfile: """
			FROM alpine:latest@sha256:ab00606a42621fb68f2ed6ad3c88be54397f981a7b70a79db3d1172b11c4367d
			COPY foo /override
			"""
	},
	dagger.#Exec & {
		args: ["sh", "-c", "test $(cat /override) = foobar"]
	}
]

TestDockerfilePath: #dagger: compute: [
	dagger.#DockerBuild & {
		context: TestData
		dockerfilePath: "./dockerfilepath/Dockerfile.custom"
	},
	dagger.#Exec & {
		args: ["sh", "-c", "test $(cat /test) = dockerfilePath"]
	}
]

TestBuildArgs: #dagger: compute: [
	dagger.#DockerBuild & {
		dockerfile: """
			FROM alpine:latest@sha256:ab00606a42621fb68f2ed6ad3c88be54397f981a7b70a79db3d1172b11c4367d
			ARG TEST=foo
			RUN test "${TEST}" = "bar"
			"""
		buildArg: TEST: "bar"
	}
]

// FIXME: this doesn't test anything beside not crashing
TestBuildLabels: #dagger: compute: [
	dagger.#DockerBuild & {
		dockerfile: """
			FROM alpine:latest@sha256:ab00606a42621fb68f2ed6ad3c88be54397f981a7b70a79db3d1172b11c4367d
			"""
		label: FOO: "bar"
	}
]

// FIXME: this doesn't test anything beside not crashing
TestBuildPlatform: #dagger: compute: [
	dagger.#DockerBuild & {
		dockerfile: """
			FROM alpine:latest@sha256:ab00606a42621fb68f2ed6ad3c88be54397f981a7b70a79db3d1172b11c4367d
			"""
		platforms: ["linux/amd64"]
	}
]
