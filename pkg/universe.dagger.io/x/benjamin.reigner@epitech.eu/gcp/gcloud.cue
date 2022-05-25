package gcp

import (
	"universe.dagger.io/alpine"
	"universe.dagger.io/docker"
)

// The gcloud tool, it allows us to have a simple container with the gcloud tool and an authenticated user
#GCloud: {
	config:  #Config
	version: string | *"380.0.0"
	packages: [pkgName=string]: {
		version: string | *""
	}

	_alpine: alpine.#Build & {
		"packages": {
			packages
			bash: {}
			python: {version: "3"}
			jq: {}
			curl: {}
			gcompat: {}
		}
	}

	_gcloud: docker.#Run & {
		env: {
			VERSION: version
			PROJECT: config.project
			REGION: config.region
			ZONE: config.zone
		}
		input: _alpine.output
		mounts: {
			source: {
				dest:     "/service_key"
				contents: config.serviceKey
			}
		}
		command: {
			name: "bash"
			args: [
				"-c",
				"""
curl -sfL https://dl.google.com/dl/cloudsdk/channels/rapid/downloads/google-cloud-sdk-${VERSION}-linux-x86_64.tar.gz | tar -C /usr/local -zx
ln -s /usr/local/google-cloud-sdk/bin/gcloud /usr/local/bin
ln -s /usr/local/google-cloud-sdk/bin/gsutil /usr/local/bin
gcloud -q auth activate-service-account --key-file=/service_key
gcloud -q config set project ${PROJECT}
gcloud -q config set compute/region ${REGION}
gcloud -q config set compute/zone ${ZONE}
""",
			]
		}
	}

	output: _gcloud.output
}
