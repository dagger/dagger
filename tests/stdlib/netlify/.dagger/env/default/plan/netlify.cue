package main

import (
	"dagger.io/dagger/op"
	"dagger.io/alpine"
	"dagger.io/netlify"
)

TestNetlify: {
	random: #Random & {}

	// Generate a website containing the random number
	html: #up: [
		op.#WriteFile & {
			content: random.out
			dest:    "index.html"
		},
	]

	// Deploy to netlify
	deploy: netlify.#Site & {
		contents: html
		name:     "dagger-test"
	}

	// Check if the deployed site has the random marker
	check: #up: [
		op.#Load & {
			from: alpine.#Image & {
				package: bash: "=~5.1"
				package: curl: true
			}
		},
		op.#Exec & {
			args: [
				"/bin/bash",
				"--noprofile",
				"--norc",
				"-eo",
				"pipefail",
				"-c",
				#"""
        test "$(curl \#(deploy.deployUrl))" = "\#(random.out)"
        """#,
			]
		},
	]
}
