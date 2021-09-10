package argocd

import (
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/os"
)

// Create an ArgoCD application
#App: {
	// ArgoCD configuration
	config: #Config

	// App name
	name: dagger.#Input & {string}

	// Repository url (git or helm)
	repo: dagger.#Input & {string}

	// Folder to deploy
	path: dagger.#Input & {"." | string}

	// Destination server
	server: dagger.#Input & {*"https://kubernetes.default.svc" | string}

	// Destination namespace
	namespace: dagger.#Input & {*"default" | string}

	os.#Container & {
		image: #CLI & {
			"config": config
		}
		command: #"""
				argocd app create "$APP_NAME" \
					--repo "$APP_REPO" \
					--path "$APP_PATH" \
					--dest-server "$APP_SERVER" \
					--dest-namespace "$APP_NAMESPACE"
			"""#
		always: true
		env: {
			APP_NAME:      name
			APP_REPO:      repo
			APP_PATH:      path
			APP_SERVER:    server
			APP_NAMESPACE: namespace
		}
	}
}
