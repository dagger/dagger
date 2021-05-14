package main

import (
	"dagger.io/aws/s3"
	"dagger.io/dagger"
)

// Name of the S3 bucket to use
bucket: string | *"dagger-io-examples"

// Website contents
source: dagger.#Artifact

// URL of the deployed website
url: "\(deploy.url)index.html"

deploy: s3.#Put & {
	"source":    source
	contentType: "text/html"
	target:      "s3://\(bucket)/"

	// Default to AWS region us-east-1
	*{
		config: region: "us-east-1"
	} | {}
}
