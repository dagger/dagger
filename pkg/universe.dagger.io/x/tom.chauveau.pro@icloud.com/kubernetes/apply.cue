package kubernetes

import (
	"dagger.io/dagger"
	"universe.dagger.io/docker"
)

_#Location: "directory" | "url"

#Apply: {
	// Kubeconfig
	kubeconfig: dagger.#Secret

	// location of manifest
	location: _#Location

	// Namespace to apply config
	namespace: *"default" | string

	{
		location: "directory"

		source: dagger.#FS

		// Customize docker.#Run
		command: flags: "-f": "/manifest"

		mounts: manifest: {
			type:     "fs" // Resolve disjunction
			dest:     "/manifest"
			contents: source
		}
	} | {
		location: "url"

		url: string

		// Customize docker.#Run
		command: {
			flags: "-f": url
		}
	}

	_baseImage: #Kubectl

	docker.#Run & {
		user:  "root"
		input: *_baseImage.output | docker.#Image
		command: {
			name: "apply"
			flags: {
				"--namespace": namespace
				"-R":          true
			}
		}
		mounts: "kubeconfig": {
			dest:     "/.kube/config"
			contents: kubeconfig
		}
	}
}
