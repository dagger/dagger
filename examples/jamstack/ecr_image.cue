package main

import (
	"dagger.io/dagger"
	"dagger.io/dagger/op"
	"dagger.io/aws"
	"dagger.io/aws/ecr"
)

// Build an image and push it to ECR
#ECRImage: {
	source: dagger.#Artifact
	// Path of the Dockerfile
	dockerfilePath?: string
	repository:      string
	tag:             string
	awsConfig:       aws.#Config
	buildArgs: [string]: string

	pushTarget: "\(repository):\(tag)"

	// Build the image
	buildImage: op.#DockerBuild & {
		context: source
		if dockerfilePath != _|_ {
			"dockerfilePath": dockerfilePath
		}
		buildArg: buildArgs
	}

	// Use these credentials to push
	ecrCreds: ecr.#Credentials & {
		config: awsConfig
		target: pushTarget
	}

	push: #up: [
		op.#DockerBuild & {
			context: source
			if dockerfilePath != _|_ {
				"dockerfilePath": dockerfilePath
			}
			buildArg: buildArgs
		},
		op.Export & {
			format: "string"
			source: op.#PushContainer & {
				ref: pushTarget
			}
		},
	]

	// FIXME: ref does not include the sha256: https://github.com/dagger/dagger/issues/303
	ref: pushTarget
}
