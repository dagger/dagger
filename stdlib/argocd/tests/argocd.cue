package argocd

import (
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/os"
)

TestConfig: argocdConfig: #Config & {
	version: dagger.#Input & {*"v2.0.5" | string}
	server:  dagger.#Input & {*"dagger-example-argocd-server.tld" | string}
	token:   dagger.#Input & {dagger.#Secret}
}

TestArgoCD: os.#Container & {
	image: #CLI & {
		config: TestConfig.argocdConfig
	}
	always: true
	command: #"""
			argocd version --output json | jq -e 'all(.client.Version; startswith("$VERSION"))'
		"""#
	env: VERSION: TestConfig.argocdConfig.version
}

TestArgoCDStatus: #Status & {
	config: TestConfig.argocdConfig
	name:   "test"
}
