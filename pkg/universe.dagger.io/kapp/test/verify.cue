package kapp

import (
	"alpha.dagger.io/dagger/op"
	"github.com/renuy/kapp"
)

#VerifyApply: {

	namespace: string

	app: string

	// Verify that app exists by listing
	#ListApps:
		"""
        kapp ls
    """

	// delete the app for other tests test
	#DeleteApp:
		"""
        kapp delete -a ${APP_NAME}
    """

	#up: [
		op.#Load & {
			from: kapp.#Kapp
		},

		op.#WriteFile & {
			dest:    "/kubeconfig"
			content: TestKubeconfig
			mode:    0o600
		},

		op.#WriteFile & {
			dest:    "/listApps.sh"
			content: #ListApps
		},

		// Check apps
		op.#Exec & {
			always: true
			args: [
				"/bin/bash",
				"--noprofile",
				"--norc",
				"-eo",
				"pipefail",
				"/listApps.sh",
			]
			env: {
				KUBECONFIG:     "/kubeconfig"
				KAPP_NAMESPACE: namespace
				APP_NAME: app
			}
		},

		op.#WriteFile & {
			dest:    "/deleteApp.sh"
			content: #DeleteApp
		},

		op.#Exec & {
			always: true
			args: [
				"/bin/bash",
				"--noprofile",
				"--norc",
				"-eo",
				"pipefail",
				"/deleteApp.sh",
			]
			env: {
				KUBECONFIG:     "/kubeconfig"
				KAPP_NAMESPACE: namespace
			}
		},
	]
}
