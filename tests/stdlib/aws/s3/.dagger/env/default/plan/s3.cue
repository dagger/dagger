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

TestS3UploadFile: {
	deploy: s3.#Put & {
		config:       TestConfig.awsConfig
		sourceInline: content
		target:       "s3://\(bucket)/test.txt"
	}

	verify: #VerifyS3 & {
		config: TestConfig.awsConfig
		target: deploy.target
		file: "test.txt"
	}
}

TestDirectory: dagger.#Artifact

TestS3UploadDir: {
	deploy: s3.#Put & {
		config: TestConfig.awsConfig
		source: TestDirectory
		target: "s3://\(bucket)/"
	}

	verifyFile: #VerifyS3 & {
		config: TestConfig.awsConfig
		target: deploy.target
		file: "dirFile.txt"
	}

	verifyDir: #VerifyS3 & {
		config: TestConfig.awsConfig
		target: deploy.target
		file: "foo.txt"
	}
}
