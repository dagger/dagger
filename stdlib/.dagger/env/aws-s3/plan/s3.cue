package s3

import (
	"dagger.io/dagger"
	"dagger.io/aws"
	"dagger.io/aws/s3"
)

TestConfig: awsConfig: aws.#Config & {
	region: "us-east-2"
}

bucket: "dagger-ci"

content: "A simple test sentence"

TestDirectory: dagger.#Artifact

TestS3Object: {
	deploy: s3.#Object & {
		always: true
		config: TestConfig.awsConfig
		source: TestDirectory
		target: "s3://\(bucket)/"
	}

	verifyFile: #VerifyS3 & {
		config: TestConfig.awsConfig
		target: deploy.target
		file:   "dirFile.txt"
	}

	verifyDir: #VerifyS3 & {
		config: TestConfig.awsConfig
		target: deploy.target
		file:   "foo.txt"
	}
}
