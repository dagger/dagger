// Kapp cli tools
package kapp

import (
	"alpha.dagger.io/dagger/op"
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/alpine"
)

// Install Kapp cli
#Kapp: {

	// Kapp version
	version: dagger.#Input & {*"v0.46.0" | string}

	#code: #"""
		[ -e /usr/local/bin/kapp ] || {
			curl -sfL https://github.com/vmware-tanzu/carvel-kapp/releases/download/${KAPP_VERSION}/kapp-linux-amd64 -o /usr/local/bin/kapp \
			&& chmod +x /usr/local/bin/kapp \
			&& kapp version \
			&& echo 'kapp installed'
		}
		"""#

	#up: [
		op.#Load & {
			from: alpine.#Image & {
				package: bash: true
				package: jq:   true
				package: curl: true
			}
		},
		op.#WriteFile & {
			dest:    "/entrypoint.sh"
			content: #code
		},
		op.#Exec & {
			args: [
				"/bin/bash",
				"--noprofile",
				"--norc",
				"-eo",
				"pipefail",
				"/entrypoint.sh",
			]
			env: KAPP_VERSION: version
		},
	]
}

// kapp deploy command
#Deploy: {

	// Version of kubectl client
	version: dagger.#Input & {*"v0.46.0" | string}

	// Kubernetes config to deploy
	source: dagger.#Input & {*null | dagger.#Artifact}

	// Kubernetes manifest to deploy inlined in a string
	manifest: dagger.#Input & {*null | string}

	// Kubernetes manifest url to deploy remote configuration
	url: dagger.#Input & {*null | string}

	// Kubernetes Namespace to deploy to
	namespace: dagger.#Input & {*"default" | string}

	//kapp app name
	app:  dagger.#Input & {*"test" | string}

	// Kube config file
	kubeconfig: dagger.#Input & {string | dagger.#Secret}

	#code: #"""
		echo 'kapp deploy'
		if [ -d /source ] || [ -f /source ]; then
			echo 'kapp-deploy-source'
			kapp deploy -n "$KAPP_NAMESPACE" -a "$APP_NAME" -f /source -y
			exit 0
		fi
		if [ -n "$DEPLOYMENT_URL" ]; then
			echo 'kapp-deploy-url'
			kapp deploy -n "$KAPP_NAMESPACE" -a "$APP_NAME" -f "$DEPLOYMENT_URL" -y
			exit 0
		fi
		"""#

	#up: [
		op.#Load & {
			from: #Kapp & {"version": version}
		},
		op.#WriteFile & {
			dest:    "/entrypoint.sh"
			content: #code
		},

		if (kubeconfig & string) != _|_ {
			op.#WriteFile & {
				dest:    "/kubeconfig"
				content: kubeconfig
				mode:    0o600
			}
		},

		if manifest != null {
			op.#WriteFile & {
				dest:    "/source"
				content: manifest
			}
		},
		op.#Exec & {
			always: true
			args: [
				"/bin/bash",
				"--noprofile",
				"--norc",
				"-eo",
				"pipefail",
				"/entrypoint.sh",
			]
			env: {
				KUBECONFIG:     "/kubeconfig"
				KAPP_NAMESPACE: namespace
				APP_NAME: app
				if url != null {
					DEPLOYMENT_URL: url
				}
			}
			if manifest == null && source != null {
				mount: "/source": from: source
			}
			if (kubeconfig & dagger.#Secret) != _|_ {
				mount: "/kubeconfig": secret: kubeconfig
			}
		},
	]
}
