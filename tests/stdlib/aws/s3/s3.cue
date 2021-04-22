package s3

import (
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

	verify: #VerifyS3
}
