package s3

import (
	"dagger.io/aws"
	"dagger.io/alpine"
	"dagger.io/dagger/op"
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
				always: true
				env: ENDPOINT_URL: config.endpointURL
				args: [
					"/bin/bash",
					"--noprofile",
					"--norc",
					"-eo",
					"pipefail",
					"-c",
					#"""
					sleep 2
					if [ -n "$ENDPOINT_URL" ]; then
						aws s3 --endpoint-url="$ENDPOINT_URL" ls --recursive \#(target) > /contents
					else
						aws s3 ls --recursive \#(target) > /contents
					fi
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
