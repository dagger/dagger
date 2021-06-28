package s3

import (
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/aws"
)

TestConfig: awsConfig: aws.#Config & {
	region: "us-east-2"
}

bucket: "dagger-ci"

content: "A simple test sentence"

TestDirectory: dagger.#Artifact

TestS3Object: {
	deploy: #Object & {
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
