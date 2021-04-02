package main

import (
	"dagger.io/llb"
	"dagger.io/alpine"
)

TestPushContainer: {
	// Generate a random number
	random: {
		string
		#up: [
			llb.#Load & {from: alpine.#Image},
			llb.#Exec & {
				args: ["sh", "-c", "echo -n $RANDOM > /rand"]
			},
			llb.#Export & {
				source: "/rand"
			},
		]
	}

	// Push an image with a random tag
	push: {
		ref: "daggerio/ci-test:\(random)"
		#up: [
			llb.#WriteFile & {
				content: random
				dest:    "/rand"
			},
			llb.#PushContainer & {
				"ref": ref
			},
		]
	}

	// Pull the image back
	pull: #up: [
		llb.#FetchContainer & {
			ref: push.ref
		},
	]

	// Check the content
	check: #up: [
		llb.#Load & {from: alpine.#Image},
		llb.#Exec & {
			args: [
				"sh", "-c", #"""
                test "$(cat /src/rand)" = "\#(random)"
                """#,
			]
			mount: "/src": from: pull
		},
	]
}
