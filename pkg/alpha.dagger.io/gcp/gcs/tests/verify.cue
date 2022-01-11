package gcs

import (
	"alpha.dagger.io/alpine"
	"alpha.dagger.io/dagger/op"
	"alpha.dagger.io/gcp"
)

#List: {
	// GCP Config
	config: gcp.#Config

	// Target GCP URL (e.g. gs://<bucket-name>/<path>/<sub-path>)
	target: string

	// URL: dummy URL, used to force a dependency
	url: string

	contents: {
		string

		#up: [
			op.#Load & {
				from: gcp.#GCloud & {
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
					gsutil ls -r \#(target) > /contents
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

#VerifyGCS: {
	file:   string
	config: gcp.#Config
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
				package: bash: true
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
