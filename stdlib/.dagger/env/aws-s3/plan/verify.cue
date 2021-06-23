package s3

import (
	"alpha.dagger.io/aws"
	"alpha.dagger.io/alpine"
	"alpha.dagger.io/dagger/op"
)

#List: {
	// AWS Config
	config: aws.#Config

	// Target S3 URL (e.g. s3://<bucket-name>/<path>/<sub-path>)
	target?: string

	contents: {
		string

		#up: [
			op.#Load & {
				from: aws.#CLI & {
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
					aws s3 ls --recursive \#(target) > /contents
					"""#,
				]
			},

			op.#Export & {
				source: "/contents"
				format: "string"
			},
		]
	}
}

#VerifyS3: {
	file:   string
	config: aws.#Config
	target: string

	lists: #List & {
		"config": config
		"target": target
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
