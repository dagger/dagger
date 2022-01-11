package cloudrun

import (
	"alpha.dagger.io/gcp"
	"alpha.dagger.io/gcp/cloudrun"
)

TestConfig: gcpConfig: gcp.#Config

TestCloudRun: deploy: cloudrun.#Service & {
	config: TestConfig.gcpConfig
	name:   "todoapp"
	image:  "gcr.io/dagger-ci/todoapp:latest"
	env: {
		FOO: "foo"
		BAR: "bar"
	}
}
