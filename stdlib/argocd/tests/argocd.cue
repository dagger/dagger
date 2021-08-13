package app

import (
	"alpha.dagger.io/argocd"
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/dagger/op"
)

TestConfig: argocdConfig: argocd.#Config & {
	version: "v2.0.5"
	server:  "dagger-example-argocd-server.tld"
	token:   dagger.#Secret & dagger.#Input
}

TestArgocd: #up: [
	// Initialize ArgoCD CLI binary
	op.#Load & {
		from: argocd.#CLI & {
			config: TestConfig.argocdConfig
		}
	},

	// Check the binary and its version
	op.#Exec & {
		args: [
			"sh", "-c",
			#"""
				argocd version --output json | jq -e 'all(.client.Version; startswith("$VERSION"))'
				"""#,
		]
		env: VERSION: TestConfig.argocdConfig.version
	},
]
