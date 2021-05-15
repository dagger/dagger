package main

import (
	"dagger.io/aws"
	"dagger.io/aws/s3"
	"dagger.io/dagger"
)

// AWS Config for credentials and default region
awsConfig: aws.#Config & {
	region: *"us-east-1" | string @dagger(input)
	accessKey: dagger.#Secret @dagger(input)
	secretKey: dagger.#Secret @dagger(input)
}

// Name of the S3 bucket to use
bucket: *"dagger-io-examples" | string @dagger(input)

source: dagger.#Artifact @dagger(input)
url:    "\(deploy.url)index.html"

deploy: s3.#Put & {
	config:      awsConfig
	"source":    source
	contentType: "text/html"
	target:      "s3://\(bucket)/"
}
