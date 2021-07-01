package cloudrun

import (
	"dagger.io/gcp"
	"dagger.io/gcp/cloudrun"
)

TestConfig: gcpConfig: gcp.#Config

TestCloudRun: deploy: cloudrun.#Service & {
	config: TestConfig.gcpConfig
	name:   "cloudrun-test"
	image:  "gcr.io/dagger-ci/cloudrun-test:latest"
}
