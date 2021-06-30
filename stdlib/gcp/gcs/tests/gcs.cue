package gcs

import (
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/gcp"
)

TestConfig: gcpConfig: gcp.#Config

bucket: "dagger-ci"

TestDirectory: dagger.#Artifact

TestGCSObject: {
	deploy: #Object & {
		always: true
		config: TestConfig.gcpConfig
		source: TestDirectory
		target: "gs://\(bucket)/"
	}

	verifyFile: #VerifyGCS & {
		config: TestConfig.gcpConfig
		target: deploy.target
		file:   "dirFile.txt"
	}

	verifyDir: #VerifyGCS & {
		config: TestConfig.gcpConfig
		target: deploy.target
		file:   "foo.txt"
	}
}
