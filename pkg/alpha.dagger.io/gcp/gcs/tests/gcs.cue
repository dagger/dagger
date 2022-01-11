package gcs

import (
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/gcp"
	"alpha.dagger.io/random"
)

TestConfig: {
	gcpConfig: gcp.#Config
	bucket:    string @dagger(input)
}

TestDirectory: dagger.#Artifact

TestGCSObject: {
	suffix: random.#String & {
		seed: "gcs"
	}

	target: "gs://\(TestConfig.bucket)/\(suffix.out)/"

	deploy: #Object & {
		always:   true
		config:   TestConfig.gcpConfig
		source:   TestDirectory
		"target": target
	}

	verifyFile: #VerifyGCS & {
		config: TestConfig.gcpConfig
		target: deploy.target
		url:    deploy.url
		file:   "dirFile.txt"
	}

	verifyDir: #VerifyGCS & {
		config: TestConfig.gcpConfig
		target: deploy.target
		url:    deploy.url
		file:   "foo.txt"
	}
}
