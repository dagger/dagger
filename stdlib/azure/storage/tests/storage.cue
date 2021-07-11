package storage

import (
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/azure"
	"alpha.dagger.io/random"
)

TestConfig: azureConfig: azure.#Config & {
	region: "us-east-2"
}

bucket: "dagger-ci"

content: "A simple test sentence"

TestDirectory: dagger.#Artifact

TestS3Object: {
	suffix: random.#String & {
		seed: "s3"
	}

	target: "s3://\(bucket)/\(suffix.out)/"

	deploy: #Object & {
		always:   true
		config:   TestConfig.awsConfig
		source:   TestDirectory
		"target": target
	}

	verifyFile: #VerifyS3 & {
		config: TestConfig.awsConfig
		target: deploy.target
		url:    deploy.url
		file:   "dirFile.txt"
	}

	verifyDir: #VerifyS3 & {
		config: TestConfig.awsConfig
		target: deploy.target
		url:    deploy.url
		file:   "foo.txt"
	}
}
