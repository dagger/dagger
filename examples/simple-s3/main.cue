package main

import (
	"dagger.io/aws"
	"dagger.io/aws/s3"
)

// AWS Config for credentials and default region
awsConfig: aws.#Config & {
	region: *"us-east-1" | string
}

// Name of the S3 bucket to use
bucket: *"hello-s3.infralabs.io" | string

name: string | *"world"

page: """
    <html>
    </head>
    <title>Simple static website on S3</title>
    </head>
    <h1>Hello!</h1>
    <li>Hey \(name)</li>
    </html>
    """

deploy: s3.#Put & {
	config:       awsConfig
	sourceInline: page
	contentType:  "text/html"
	target:       "s3://\(bucket)/index.html"
}
