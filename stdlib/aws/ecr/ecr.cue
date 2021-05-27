package ecr

import (
	"dagger.io/dagger"
	"dagger.io/dagger/op"
	"dagger.io/aws"
)

// Credentials retriever for ECR
#Credentials: {
	// AWS Config
	config: aws.#Config

	out: dagger.#Secret

	// ECR credentials
	username: "AWS"

	secret: {
		@dagger(output)
		string

		#up: [
			op.#Load & {
				from: aws.#CLI & {
					"config": config
				}
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
					#"""
					aws ecr get-login-password > /out
					"""#
				]
			},

			op.#Export & {
				source: "/out"
				format: "string"
			}
		]
	}
}
