package app

import (
	"alpha.dagger.io/argocd"
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/os"
)

TestConfig: argocdConfig: argocd.#Config & {
	version: "v2.0.5"
	server:  "dagger-example-argocd-server.tld"
	token:   dagger.#Secret & dagger.#Input
}

TestArgoCD2: os.#Container & {
	image: argocd.#CLI & {
		config: TestConfig.argocdConfig
	}
	always: true
	command: #"""
			argocd version --output json | jq -e 'all(.client.Version; startswith("$VERSION"))'
		"""#
	env: VERSION: TestConfig.argocdConfig.version
}
