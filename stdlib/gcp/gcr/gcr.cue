package gcr

import (
	"dagger.io/dagger/op"
	"dagger.io/gcp"
)

// Credentials retriever for GCR
#Credentials: {
	// GCP Config
	config: gcp.#Config

	// GCR credentials
	username: "oauth2accesstoken"
	secret: {
		string

		#up: [
			op.#Load & {
				from: gcp.#GCloud & {
					"config": config
				}
			},
			op.#Exec & {
				always: true
				args: [
					"/bin/bash",
					"--noprofile",
					"--norc",
					"-eo",
					"pipefail",
					"-c",
					#"""
						printf $(gcloud auth print-access-token) > /token.txt
						"""#,
				]
			},

			op.#Export & {
				source: "/token.txt"
			},
		]
	} @dagger(output)
}
