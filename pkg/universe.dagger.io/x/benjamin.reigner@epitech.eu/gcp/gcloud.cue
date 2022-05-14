package gcp

import (
	"universe.dagger.io/alpine"
	"universe.dagger.io/docker"
)

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
				#"""
curl -sfL https://dl.google.com/dl/cloudsdk/channels/rapid/downloads/google-cloud-sdk-\#(version)-linux-x86_64.tar.gz | tar -C /usr/local -zx
ln -s /usr/local/google-cloud-sdk/bin/gcloud /usr/local/bin
ln -s /usr/local/google-cloud-sdk/bin/gsutil /usr/local/bin
gcloud -q auth activate-service-account --key-file=/service_key
gcloud -q config set project \#(config.project)
gcloud -q config set compute/region \#(config.region)
gcloud -q config set compute/zone \#(config.zone)
"""#,
			]
		}
	}

	output: _gcloud.output
}
