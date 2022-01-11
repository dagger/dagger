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

	// Basic authentification to login
	basicAuth: {
		// Username
		username: dagger.#Input & {string}

		// Password
		password: dagger.#Input & {dagger.#Secret}
	} | *null

	// ArgoCD authentication token
	token: dagger.#Input & {*null | dagger.#Secret}
}

// Re-usable CLI component
#CLI: {
	config: #Config

	#up: [
		op.#Load & {
			from: alpine.#Image & {
				package: bash: true
				package: jq:   true
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

		if config.basicAuth != null && config.token == null {
			// Login to ArgoCD server
			op.#Exec & {
				args: ["sh", "-c", #"""
						argocd login "$ARGO_SERVER" --username "$ARGO_USERNAME" --password $(cat /run/secrets/password) --insecure
					"""#,
				]
				env: {
					ARGO_SERVER:   config.server
					ARGO_USERNAME: config.basicAuth.username
				}
				mount: "/run/secrets/password": secret: config.basicAuth.password
			}
		},

		if config.token != null && config.basicAuth == null {
			// Write config file
			op.#Exec & {
				args: ["sh", "-c",
					#"""
						mkdir -p ~/.argocd && cat > ~/.argocd/config << EOF
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
			}
		},

	]
}
