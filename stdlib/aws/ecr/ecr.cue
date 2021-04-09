package ecr

import (
	"dagger.io/dagger"
	"dagger.io/aws"
)

// Credentials retriever for ECR
#Credentials: {

	// AWS Config
	config: aws.#Config

	// Target is the ECR image
	target: string

	out: dagger.#Secret

	// ECR credentials
	credentials: dagger.#RegistryCredentials & {
		username: "AWS"
		secret: out
	}

	aws.#Script & {
		"config": config
		export: "/out"
		code: """
			aws ecr get-login-password > /out
			"""
	}

	// Authentication for ECR Registries
	auth: dagger.#RegistryAuth
	auth: "\(target)": credentials
}
