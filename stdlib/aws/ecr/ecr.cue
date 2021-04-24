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
	// FIXME Exected twice and trigger error : "TestECR.creds.secret: 2 errors in empty disjunction"
	// Happend because of v0.8.3 of buildkit container
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
