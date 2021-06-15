// Google Container Registry
package gcr

import (
	"dagger.io/dagger/op"
	"dagger.io/gcp"
)

// Credentials retriever for GCR
#Credentials: {
	// GCP Config
	config: gcp.#Config

	// GCR registry username
	username: "oauth2accesstoken" @dagger(output)

	// GCR registry secret
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
