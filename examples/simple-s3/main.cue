package main

import (
	"dagger.io/aws"
	"dagger.io/aws/s3"
	"dagger.io/dagger"
)

// AWS Config for credentials and default region
awsConfig: aws.#Config & {
	region: *"us-east-1" | string
}

// Name of the S3 bucket to use
bucket: *"dagger-io-examples" | string

source: dagger.#Artifact
url:    "\(deploy.url)index.html"

deploy: s3.#Put & {
	config:      awsConfig
	"source":    source
	contentType: "text/html"
	target:      "s3://\(bucket)/"
}
