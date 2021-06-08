package helm

import (
	"strconv"

	"dagger.io/dagger"
	"dagger.io/dagger/op"
	"dagger.io/kubernetes"
)

// Install a Helm chart
#Chart: {
	// Helm deployment name
	name: string @dagger(input)

	// Helm chart to install from source
	chartSource?: dagger.#Artifact @dagger(input)

	// Helm chart to install from repository
	chart?: string @dagger(input)

	// Helm chart repository
	repository?: string @dagger(input)

	// Helm values (either a YAML string or a Cue structure)
	values?: string @dagger(input)

	// Kubernetes Namespace to deploy to
	namespace: string @dagger(input)

	// Helm action to apply
	action: *"installOrUpgrade" | "install" | "upgrade" @dagger(input)

	// time to wait for any individual Kubernetes operation (like Jobs for hooks)
	timeout: string | *"5m" @dagger(input)

	// if set, will wait until all Pods, PVCs, Services, and minimum number of
	// Pods of a Deployment, StatefulSet, or ReplicaSet are in a ready state
	// before marking the release as successful.
	// It will wait for as long as timeout
	wait: *true | bool @dagger(input)

	// if set, installation process purges chart on fail.
	// The wait option will be set automatically if atomic is used
	atomic: *true | bool @dagger(input)

	// Kube config file
	kubeconfig: dagger.#Secret @dagger(input)

	// Helm version
	version: *"3.5.2" | string @dagger(input)

	// Kubectl version
	kubectlVersion: *"v1.19.9" | string @dagger(input)

	#up: [
		op.#Load & {
			from: kubernetes.#Kubectl & {
				version: kubectlVersion
			}
		},
		op.#Exec & {
			env: HELM_VERSION: version
			args: [
				"/bin/bash",
				"--noprofile",
				"--norc",
				"-eo",
				"pipefail",
				"-c",
				#"""
					# Install Yarn
					curl -sfL -S https://get.helm.sh/helm-v${HELM_VERSION}-linux-amd64.tar.gz | \
					    tar -zx -C /tmp && \
					    mv /tmp/linux-amd64/helm /usr/local/bin && \
					    chmod +x /usr/local/bin/helm
					"""#,
			]
		},
		op.#Mkdir & {
			path: "/helm"
		},
		op.#WriteFile & {
			dest:    "/entrypoint.sh"
			content: #code
		},
		if chart != _|_ {
			op.#WriteFile & {
				dest:    "/helm/chart"
				content: chart
			}
		},
		if (values & string) != _|_ {
			op.#WriteFile & {
				dest:    "/helm/values.yaml"
				content: values
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
				KUBE_NAMESPACE: namespace

				if repository != _|_ {
					HELM_REPO: repository
				}
				HELM_NAME:    name
				HELM_ACTION:  action
				HELM_TIMEOUT: timeout
				HELM_WAIT:    strconv.FormatBool(wait)
				HELM_ATOMIC:  strconv.FormatBool(atomic)
			}
			mount: {
				if chartSource != _|_ && chart == _|_ {
					"/helm/chart": from: chartSource
				}
				"/kubeconfig": secret: kubeconfig
			}
		},
	]
}
