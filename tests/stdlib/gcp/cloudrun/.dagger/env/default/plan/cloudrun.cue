package cloudrun

import (
	"dagger.io/gcp"
	"dagger.io/gcp/cloudrun"
)

TestConfig: gcpConfig: gcp.#Config

TestCloudRun: deploy: cloudrun.#Deploy & {
	config:      TestConfig.gcpConfig
	serviceName: "cloudrun-test"
	region:      "us-west2"
	image:       "gcr.io/dagger-ci/cloudrun-test:latest"
}
