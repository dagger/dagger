package ecr

import (
	"dagger.io/dagger"
	"dagger.io/aws"
)

// Credentials retriever for ECR
#Credentials: {
	// AWS Config
	config: aws.#Config

	out: dagger.#Secret

	// ECR credentials
	username: "AWS"

	secret: out

	aws.#Script & {
		always:   true
		"config": config
		export:   "/out"
		code: """
			aws ecr get-login-password > /out
			"""
	}
}
