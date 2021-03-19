package netlify

import (
	"dagger.io/llb"
	"dagger.io/alpine"
	"dagger.io/netlify"
)

TestNetlify: {
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

	// Generate a website containing the random number
	html: #compute: [
		llb.#WriteFile & {
			content: random
			dest:    "index.html"
		},
	]

	// Deploy to netlify
	deploy: netlify.#Site & {
		contents: html
		name:     "dagger-test"
	}

	// Check if the deployed site has the random marker
	check: #compute: [
		llb.#Load & {
			from: alpine.#Image & {
				package: bash: "=~5.1"
				package: curl: "=~7.74"
			}
		},
		llb.#Exec & {
			args: [
				"/bin/bash",
				"--noprofile",
				"--norc",
				"-eo",
				"pipefail",
				"-c",
				#"""
                test "$(curl \#(deploy.url))" = "\#(random)"
                """#,
			]
		},
	]
}
