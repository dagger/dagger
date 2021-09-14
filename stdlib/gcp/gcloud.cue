package gcp

import (
	"alpha.dagger.io/dagger/op"
	"alpha.dagger.io/alpine"
)

// Re-usable gcloud component
#GCloud: {
	config:  #Config
	version: string | *"288.0.0"
	package: [string]: string | bool

	#up: [
		op.#Load & {
			from: alpine.#Image & {
				"package": package
				"package": bash:    "=~5.1"
				"package": python3: "=~3.8"
				"package": jq:      "=~1.6"
				"package": curl:    true
			}
		},

		// Install the gcloud cli
		op.#Exec & {
			args: ["sh", "-c",
				#"""
                curl -sfL https://dl.google.com/dl/cloudsdk/channels/rapid/downloads/google-cloud-sdk-\#(version)-linux-x86_64.tar.gz | tar -C /usr/local -zx
                ln -s /usr/local/google-cloud-sdk/bin/gcloud /usr/local/bin
                ln -s /usr/local/google-cloud-sdk/bin/gsutil /usr/local/bin
                """#,
			]
		},

		op.#Exec & {
			args: ["gcloud", "-q", "auth", "activate-service-account", "--key-file=/service_key"]
			mount: "/service_key": secret: config.serviceKey
		},

		op.#Exec & {
			args: ["gcloud", "-q", "config", "set", "project", config.project]
		},

		if config.region != null {
			op.#Exec & {
				args: ["gcloud", "-q", "config", "set", "compute/region", config.region]
			}
		},
		if config.zone != null {
			op.#Exec & {
				args: ["gcloud", "-q", "config", "set", "compute/zone", config.zone]
			}
		},
	]
}
