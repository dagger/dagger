package main

import (
	"dagger.io/aws"
	"dagger.io/aws/s3"
	"dagger.io/dagger"
)

// AWS Config for credentials and default region
awsConfig: aws.#Config & {
	region: *"us-east-1" | string @dagger(input)
}

// Name of the S3 bucket to use
bucket: *"dagger-io-examples" | string @dagger(input)

// Source code to deploy
source: dagger.#Artifact @dagger(input)
url:    "\(deploy.url)index.html"

deploy: s3.#Put & {
	always:      true
	config:      awsConfig
	"source":    source
	contentType: "text/html"
	target:      "s3://\(bucket)/"
}
