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

	// ArgoCD authentication token
	token: dagger.#Secret & dagger.#Input
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

		// Write config file
		op.#Exec & {
			args: ["sh", "-c",
				#"""
					mkdir ~/.argocd && cat > ~/.argocd/config << EOF
					contexts:
					- name: "$SERVER"
					  server: "$SERVER"
					  user: "$SERVER"
					current-context: "$SERVER"
					servers:
					- grpc-web-root-path: ""
					  server: "$SERVER"
					users:
					- auth-token: $(cat /run/secrets/token)
					  name: "$SERVER"
					EOF
					"""#,
			]
			mount: "/run/secrets/token": secret: config.token
			env: SERVER: config.server
		},
	]
}
