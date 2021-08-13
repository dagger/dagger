// ArgoCD applications
package app

import (
	"alpha.dagger.io/argocd"
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/dagger/op"
)

// Get an application
#Application: {
	config: argocd.#Config

	// ArgoCD application
	name: dagger.#Input & {string}

	// ArgoCD CLI output
	outputs: {
		// Application health
		health: dagger.#Output & {string}

		// Application sync state
		sync: dagger.#Output & {string}

		// Namespace
		namespace: dagger.#Output & {string}

		// Server
		server: dagger.#Output & {string}

		// Comma separated list of application URLs
		urls: dagger.#Output & {string}

		// Last operation state message
		state: dagger.#Output & {string}
	}

	outputs: #up: [
		op.#Load & {
			from: argocd.#CLI & {
				"config": config
			}
		},

		op.#Exec & {
			args: ["sh", "-c",
				#"""
					argocd app get "$APPLICATION" --output json | jq '{health:.status.health.status,sync:.status.sync.status,namespace:.spec.destination.namespace,server:.spec.destination.server,urls:.status.summary.externalURLs|join(","),state:.status.operationState.message}' > /output.json
					"""#,
			]
			env: APPLICATION: name
		},

		op.#Export & {
			source: "/output.json"
			format: "json"
		},
	]
}

// Sync an application to its target state
#Synchronization: {
	config: argocd.#Config

	// ArgoCD application
	application: dagger.#Input & {string}

	#up: [
		op.#Load & {
			from: argocd.#CLI & {
				"config": config
			}
		},

		op.#Exec & {
			args: [
				"sh", "-c", #"""
					argocd app sync "$APPLICATION"
					"""#,
			]
			env: APPLICATION: application
		},
	]
}

// Wait for an application to reach a synced and healthy state
#SynchronizedApplication: {
	config: argocd.#Config

	// ArgoCD application
	application: dagger.#Input & {string}

	#up: [
		op.#Load & {
			from: argocd.#CLI & {
				"config": config
			}
		},

		op.#Exec & {
			args: [
				"sh", "-c", #"""
					argocd app wait "$APPLICATION"
					"""#,
			]
			env: APPLICATION: application
		},
	]
}
