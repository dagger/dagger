package cloudrun

import (
	"dagger.io/gcp"
	"dagger.io/gcp/cloudrun"
)

TestConfig: gcpConfig: gcp.#Config & {
	region: "us-west2"
}

TestCloudRun: deploy: cloudrun.#Deploy & {
	serviceName: "cloudrun-test"
	config:      TestConfig.gcpConfig
	image:       "gcr.io/dagger-ci/cloudrun-test:latest"
}
