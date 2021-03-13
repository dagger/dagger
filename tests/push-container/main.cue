package main

import (
	"dagger.io/dagger"
	"dagger.io/alpine"
)

TestPushContainer: {
	// Generate a random number
	random: {
		string
		#compute: [
			dagger.#Load & {from: alpine.#Image},
			dagger.#Exec & {
				args: ["sh", "-c", "echo -n $RANDOM > /rand"]
			},
			dagger.#Export & {
				source: "/rand"
			},
		]
	}

	// Push an image with a random tag
	push: {
		ref: "daggerio/ci-test:\(random)"
		#compute: [
			dagger.#WriteFile & {
				content: random
				dest:    "/rand"
			},
			dagger.#PushContainer & {
				"ref": ref
			},
		]
	}

	// Pull the image back
	pull: #compute: [
		dagger.#FetchContainer & {
			ref: push.ref
		},
	]

	// Check the content
	check: #compute: [
		dagger.#Load & {from: alpine.#Image},
		dagger.#Exec & {
			args: [
				"sh", "-c", #"""
                test "$(cat /src/rand)" = "\#(random)"
                """#,
			]
			mount: "/src": from: pull
		},
	]
}
