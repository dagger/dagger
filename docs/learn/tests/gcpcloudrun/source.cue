package gcpcloudrun

import (
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/docker"
	"alpha.dagger.io/gcp"
	"alpha.dagger.io/gcp/cloudrun"
	"alpha.dagger.io/gcp/gcr"
)

// Source code of the sample application
src: dagger.#Artifact & dagger.#Input

// GCR full image name
imageRef: string & dagger.#Input

image: docker.#Build & {
	source: src
}

gcpConfig: gcp.#Config

creds: gcr.#Credentials & {
	config: gcpConfig
}

push: docker.#Push & {
	target: imageRef
	source: image
	auth: {
		username: creds.username
		secret:   creds.secret
	}
}

deploy: cloudrun.#Service & {
	config: gcpConfig
	image:  push.ref
}
