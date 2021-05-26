package gke

import (
	"dagger.io/dagger/op"
	"dagger.io/gcp"
)

// KubeConfig config outputs a valid kube-auth-config for kubectl client
#KubeConfig: {
	// GCP Config
	config: gcp.#Config

	// GKE cluster name
	clusterName: string @dagger(input)

	// Kubectl version
	version: *"v1.19.9" | string @dagger(input)

	// kubeconfig is the generated kube configuration file
	kubeconfig: {
		// FIXME There is a problem with dagger.#Secret type
		string @dagger(output)

		#up: [
			op.#Load & {
				from: gcp.#GCloud & {
					"config": config
				}
			},

			op.#WriteFile & {
				dest:    "/entrypoint.sh"
				content: #Code
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
					GKE_CLUSTER:     clusterName
					KUBECTL_VERSION: version
				}
				mount: "/cache/bin": "cache"
			},
			op.#Export & {
				source: "/kubeconfig"
				format: "string"
			},
		]
	}
}

#Code: #"""
	[ -e /cache/bin/kubectl ] || {
	   curl -sfL https://dl.k8s.io/${KUBECTL_VERSION}/bin/linux/amd64/kubectl -o /cache/bin/kubectl \
	    && chmod +x /cache/bin/kubectl
	}

	export KUBECONFIG=/kubeconfig
	export PATH="$PATH:/cache/bin"

	# Generate a kube configiration
	gcloud -q container clusters get-credentials "$GKE_CLUSTER"

	# Figure out the kubernetes username
	CONTEXT="$(kubectl config current-context)"
	USER="$(kubectl config view -o json | \
	    jq -r ".contexts[] | select(.name==\"$CONTEXT\") | .context.user")"

	# Grab a kubernetes access token
	ACCESS_TOKEN="$(gcloud -q config config-helper --format json --min-expiry 1h | \
	    jq -r .credential.access_token)"

	# Remove the user config and replace it with the token
	kubectl config unset "users.${USER}"
	kubectl config set-credentials "$USER" --token "$ACCESS_TOKEN"
	"""#
