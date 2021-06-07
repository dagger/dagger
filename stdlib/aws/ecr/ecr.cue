package ecr

import (
	"dagger.io/aws"
	"dagger.io/os"
)

// Convert AWS credentials to Docker Registry credentials for ECR
#Credentials: {
	// AWS Config
	config: aws.#Config

	// ECR credentials
	username: "AWS"

	ctr: os.#Container & {
		image: aws.#CLI & {
			"config": config
		}
		always:  true
		command: "aws ecr get-login-password > /out"
	}

	secret: {
		os.#File & {
			from: ctr
			path: "/out"
		}
	}.read.data
}
