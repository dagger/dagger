package main

import (
	"alpha.dagger.io/aws"
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/random"
	"alpha.dagger.io/aws/cloudformation"
)

// AWS account: credentials and region
awsConfig: aws.#Config

// Create a random suffix
suffix: random.#String & {
	seed: ""
}

// Query the Cloudformation stackname, or create one with a random suffix for uniqueness
cfnStackName: *"stack-\(suffix.out)" | string & dagger.#Input

// AWS Cloudformation stdlib
cfnStack: cloudformation.#Stack & {
	config:    awsConfig
	stackName: cfnStackName
	source:    template
}
