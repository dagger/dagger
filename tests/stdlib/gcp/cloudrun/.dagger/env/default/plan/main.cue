package cloudrun

import (
	"dagger.io/gcp"
	"dagger.io/gcp/cloudrun"
)

TestConfig: gcpConfig: gcp.#Config & {
	region: "us-west2"
}

TestCloudRun: deploy: cloudrun.#Deploy & {
	config:      TestConfig.gcpConfig
	serviceName: "cloudrun-test4"
	image:       "gcr.io/dagger-ci/cloudrun-test:latest"
}
