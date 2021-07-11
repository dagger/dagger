package storage

import (
	"alpha.dagger.io/azure"
	"alpha.dagger.io/alpine"
	"alpha.dagger.io/dagger/op"
)

#List: {
	// Azure Config
	config: azure.#Config

	// Target Azure storage share
	target: string

	// URL: dummy URL, used to force a dependency
	url: string

	contents: {
		string

		#up: [
			op.#Load & {
				from: azure.#CLI & {
					"config": config
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
					az storage file list --account-key accountkey --account-name accountname --share-name "$TARGET" > /contents
					"""#,
				]
				env: URL: url
			},

			op.#Export & {
				source: "/contents"
				format: "string"
			},
		]
	}
}

#VerifyAzure: {
	file:   string
	config: aws.#Config
	target: string
	url:    string

	lists: #List & {
		"config": config
		"target": target
		"url":    url
	}

	test: #up: [
		op.#Load & {
			from: alpine.#Image & {
				package: bash: "~5.1"
			}
		},

		op.#WriteFile & {
			dest:    "/test"
			content: lists.contents
		},

		op.#Exec & {
			always: true
			args: [
				"/bin/bash",
				"--noprofile",
				"--norc",
				"-eo",
				"pipefail",
				"-c",
				"grep -q \(file) /test",
			]
		},
	]
}
