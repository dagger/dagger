package main

import (
	"alpha.dagger.io/os"
	"alpha.dagger.io/aws"
	"alpha.dagger.io/dagger"
)

// Remove Cloudformation Stack
stackRemoval: {
	// Cloudformation Stackname
	stackName: string & dagger.#Input

	ctr: os.#Container & {
		image: aws.#CLI & {
			config: awsConfig
		}
		always: true
		env: STACK_NAME: stackName
		command: """
			aws cloudformation delete-stack --stack-name $STACK_NAME
			"""
	}
}
