package argocd

import (
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/os"
)

// Sync an application to its targer state
#Sync: {
	// ArgoCD configuration
	config: #Config

	// ArgoCD application
	application: dagger.#Input & {string}

	// Wait the application to sync correctly
	wait: dagger.#Input & {*false | bool}

	_ctr: os.#Container & {
		image: #CLI & {
			"config": config
		}
		command: #"""
				argocd app sync "$APPLICATION"

				if [ -n "$WAIT_FLAG" ]; then
					argocd app wait "$APPLICATION"
				fi
			"""#
		env: APPLICATION: application
		if wait {
			env: WAIT_FLAG: "wait"
		}
	}
}
