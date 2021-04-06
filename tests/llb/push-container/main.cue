package main

import (
	"dagger.io/dagger/op"
	"dagger.io/alpine"
)

TestPushContainer: {
	// Generate a random number
	random: {
		string
		#up: [
			op.#Load & {from: alpine.#Image},
			op.#Exec & {
				args: ["sh", "-c", "echo -n $RANDOM > /rand"]
			},
			op.#Export & {
				source: "/rand"
			},
		]
	}

	// Push an image with a random tag
	push: {
		ref: "daggerio/ci-test:\(random)"
		#up: [
			op.#WriteFile & {
				content: random
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
                test "$(cat /src/rand)" = "\#(random)"
                """#,
			]
			mount: "/src": from: pull
		},
	]
}
