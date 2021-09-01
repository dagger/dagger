package main

import (
	"alpha.dagger.io/aws"
	"alpha.dagger.io/aws/eks"
	"alpha.dagger.io/aws/ecr"
)

// Value created for generic reference of `kubeconfig` in `todoapp.cue`
kubeconfig: eksConfig.kubeconfig

// awsConfig for Amazon connection
awsConfig: aws.#Config

// eksConfig used for deployment
eksConfig: eks.#KubeConfig & {
	// config field references `awsConfig` value to set in once
	config: awsConfig
}

// ecrCreds used for remote image push
ecrCreds: ecr.#Credentials & {
	// config field references `awsConfig` value to set in once
	config: awsConfig
}
