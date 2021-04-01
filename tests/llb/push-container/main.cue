package main

import (
	"dagger.io/llb"
	"dagger.io/alpine"
)

TestPushContainer: {
	// Generate a random number
	random: {
		string
		#compute: [
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
		#compute: [
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
	pull: #compute: [
		llb.#FetchContainer & {
			ref: push.ref
		},
	]

	// Check the content
	check: #compute: [
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
