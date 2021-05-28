package testing

import (
	"dagger.io/dagger"

	"dagger.io/terraform"
)

TestData: dagger.#Artifact

TestConfig: awsConfig: {
	accessKey: string
	secretkey: string
	region:    "us-east-2"
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
