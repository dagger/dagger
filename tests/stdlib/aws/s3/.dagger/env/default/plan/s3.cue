package s3

import (
	"dagger.io/dagger"
	"dagger.io/aws"
	"dagger.io/aws/s3"
)

TestConfig: awsConfig: aws.#Config & {
	region:      "us-east-2"
	endpointURL: string @dagger(input)
}

bucket: "dagger-ci"

content: "A simple test sentence"

TestS3UploadFile: {
	randomFileName: random

	deploy: s3.#Put & {
		config:       TestConfig.awsConfig
		sourceInline: content
		target:       "s3://\(bucket)/\(randomFileName).txt"
	}

	verify: #VerifyS3 & {
		config: TestConfig.awsConfig
		target: deploy.target
		file:   "\(randomFileName).txt"
	}
}

TestDirectory: dagger.#Artifact

TestS3UploadDir: {
	randomDirName: random

	deploy: s3.#Put & {
		config: TestConfig.awsConfig
		source: TestDirectory
		target: "s3://\(bucket)/\(randomDirName)/"
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

TestS3Sync: {
	deploy: s3.#Sync & {
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
