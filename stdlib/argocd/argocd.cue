// ArgoCD client operations
package argocd

import (
	"alpha.dagger.io/alpine"
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/dagger/op"
)

// ArgoCD configuration
#Config: {
	// ArgoCD CLI binary version
	version: *"v2.0.5" | dagger.#Input & {string}

	// ArgoCD server
	server: dagger.#Input & {string}

	// ArgoCD project
	project: *"default" | dagger.#Input & {string}

	// Username
	username: dagger.#Input & {string}

	// Password
	password: dagger.#Input & {dagger.#Secret}
}

// Re-usable CLI component
#CLI: {
	config: #Config

	#up: [
		op.#Load & {
			from: alpine.#Image & {
				package: bash: "=~5.1"
				package: jq:   "=~1.6"
				package: curl: true
			}
		},

		// Install the ArgoCD CLI
		op.#Exec & {
			args: ["sh", "-c",
				#"""
					curl -sSL -o /usr/local/bin/argocd https://github.com/argoproj/argo-cd/releases/download/$VERSION/argocd-linux-amd64 &&
					chmod +x /usr/local/bin/argocd
					"""#,
			]
			env: VERSION: config.version
		},

		// Login to ArgoCD server
		op.#Exec & {
			args: ["sh", "-c", #"""
					argocd login "$ARGO_SERVER" --username "$ARGO_USERNAME" --password $(cat /run/secrets/password) --insecure
				"""#,
			]
			env: {
				ARGO_SERVER:   config.server
				ARGO_USERNAME: config.username
			}
			mount: "/run/secrets/password": secret: config.password
		},
	]
}
