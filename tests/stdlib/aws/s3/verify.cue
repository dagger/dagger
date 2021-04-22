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

	// Export folder
	export: "/contents"

	// Script
	aws.#Script & {
		code: """
			aws s3 ls \(target) > /contents
		"""
	}
}

#VerifyS3: {
	lists: #List & {
		config: TestConfig.awsConfig
		target: "s3://\(bucket)"
	}

	#CheckFiles:
		"""
				grep -q test.txt /test
			"""

	test: #up: [
		op.#Load & {
			from: alpine.#Image & {
				package: bash: "~5.1"
			}
		},

		op.#WriteFile & {
			dest:    "/test"
			content: lists.out
		},

		op.#WriteFile & {
			dest:    "/checkFiles.sh"
			content: #CheckFiles
		},

		op.#Exec & {
			always: true
			args: [
				"/bin/bash",
				"--noprofile",
				"--norc",
				"-eo",
				"pipefail",
				"/checkFiles.sh",
			]
		},
	]
}
