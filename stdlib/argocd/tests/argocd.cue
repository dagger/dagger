package argocd

import (
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/os"
)

TestConfig: argocdConfig: #Config & {
	version: dagger.#Input & {*"v2.0.5" | string}
	server:  dagger.#Input & {*"dagger-example-argocd-server.tld" | string}
	basicAuth: {
		username: dagger.#Input & {*"admin" | string}
		password: dagger.#Input & {dagger.#Secret}
	}
}

TestClient: os.#Container & {
	image: #CLI & {
		config: TestConfig.argocdConfig
	}
	command: #"""
			argocd account list | grep "$ARGOCD_USERNAME"
		"""#
	env: ARGOCD_USERNAME: TestConfig.argocdConfig.basicAuth.username
}

TestApp: #App & {
	config: TestConfig.argocdConfig
	name:   "daggerci-test"
	repo:   "https://github.com/argoproj/argocd-example-apps.git"
	path:   "guestbook"
}

TestArgoCDStatus: #Sync & {
	config:      TestApp.config
	application: TestApp.name
}
