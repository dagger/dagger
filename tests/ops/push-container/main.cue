package main

import (
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/dagger/op"
	"alpha.dagger.io/alpine"
	"alpha.dagger.io/random"
)

registry: {
	username: dagger.#Input & {string}
	secret:   dagger.#Input & {dagger.#Secret}
}

TestPushContainer: {
	tag: random.#String & {
		seed: "push-container"
	}

	// Push an image with a random tag
	push: {
		ref: "daggerio/ci-test:\(tag.out)"
		#up: [
			op.#DockerLogin & {
				target: ref
				registry
			},
			op.#WriteFile & {
				content: tag.out
				dest:    "/rand"
			},
			op.#PushContainer & {
				"ref": ref
			},
		]
	}

	// Pull the image back
	pull: #up: [
		op.#FetchContainer & {
			ref: push.ref
		},
	]

	// Check the content
	check: #up: [
		op.#Load & {from: alpine.#Image},
		op.#Exec & {
			args: [
				"sh", "-c", #"""
                test "$(cat /src/rand)" = "\#(tag.out)"
                """#,
			]
			mount: "/src": from: pull
		},
	]
}

// Ensures image metadata is preserved in a push
TestPushContainerMetadata: {
	tag: random.#String & {
		seed: "container-metadata"
	}

	// `docker build` using an `ENV` and push the image
	push: {
		ref: "daggerio/ci-test:\(tag.out)-dockerbuild"
		#up: [
			op.#DockerBuild & {
				dockerfile: #"""
					FROM alpine:latest@sha256:ab00606a42621fb68f2ed6ad3c88be54397f981a7b70a79db3d1172b11c4367d
					ENV CHECK \#(tag.out)
					"""#
			},
			op.#PushContainer & {
				"ref": ref
			},
		]
	}

	// Pull the image down and make sure the ENV is preserved
	check: #up: [
		op.#FetchContainer & {
			ref: push.ref
		},
		op.#Exec & {
			args: [
				"sh", "-c", #"""
                env
                test "$CHECK" = "\#(tag.out)"
                """#,
			]
		},
	]

	// Do a FetchContainer followed by a PushContainer, make sure
	// the ENV is preserved
	pullPush: {
		ref: "daggerio/ci-test:\(tag.out)-pullpush"

		#up: [
			op.#FetchContainer & {
				ref: push.ref
			},
			op.#PushContainer & {
				"ref": ref
			},
		]
	}

	pullPushCheck: #up: [
		op.#FetchContainer & {
			ref: pullPush.ref
		},
		op.#Exec & {
			args: [
				"sh", "-c", #"""
                test "$CHECK" = "\#(tag.out)"
                """#,
			]
		},
	]
}
