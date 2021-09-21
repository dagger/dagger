// Amazon Elastic Container Registry (ECR)
package ecr

import (
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/aws"
	"alpha.dagger.io/os"
)

// Convert ECR credentials to Docker Login format
#Credentials: {
	// AWS Config
	config: aws.#Config

	// ECR registry
	username: "AWS" & dagger.#Output

	ctr: os.#Container & {
		image: aws.#CLI & {
			"config": config
		}
		always:  true
		command: "aws ecr get-login-password > /out"
	}

	// ECR registry secret
	secret: {
		os.#File & {
			from: ctr
			path: "/out"
		}
	}.contents & dagger.#Output
}
