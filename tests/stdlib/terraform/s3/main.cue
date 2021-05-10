package testing

import (
	"dagger.io/dagger"

	"dagger.io/terraform"
	"dagger.io/aws"
)

TestData: dagger.#Artifact

TestConfig: awsConfig: aws.#Config & {
	region: "us-east-2"
}

TestTerraform: apply: terraform.#Configuration & {
	source: TestData
	env: {
		AWS_ACCESS_KEY_ID:     TestConfig.awsConfig.accessKey
		AWS_SECRET_ACCESS_KEY: TestConfig.awsConfig.secretKey
		AWS_DEFAULT_REGION:    TestConfig.awsConfig.region
		AWS_REGION:            TestConfig.awsConfig.region
	}
}
