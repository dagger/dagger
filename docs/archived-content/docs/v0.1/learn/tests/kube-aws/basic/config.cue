package main

import (
	"alpha.dagger.io/aws"
	"alpha.dagger.io/aws/eks"
)

// Value created for generic reference of `kubeconfig` in `todoapp.cue`
kubeconfig: eksConfig.kubeconfig

// awsConfig for Amazon connection  
awsConfig: aws.#Config

// eksConfig used for deployment  
eksConfig: eks.#KubeConfig & {
	// config field references `gkeConfig` value to set in once
	config: awsConfig
}
