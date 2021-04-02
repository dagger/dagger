package main

import (
    "encoding/yaml"
    "dagger.io/dagger"
    "dagger.io/file"
    "dagger.io/kubernetes"
)


// Fill using :
//      --input-dir kubeconfigDirectory=/path/to/the/.kube/directory/
kubeDirectory: dagger.#Artifact

// Read kubeconfig file
kubeconfigFile: file.#Read & {
    from: kubeDirectory,
    filename: "config"
}

kubeSrc: {
	apiVersion: "v1"
	kind:       "Pod"
	metadata: name: "kube-test"
	spec: {
		restartPolicy: "Never"
		containers: [{
			name:  "test"
			image: "hello-world"
		}]
	}
}

// Example of a simple `kubectl apply` using a simple config
kubeApply: kubernetes.#Apply & {
	source:     yaml.Marshal(kubeSrc) // FIXME file /source isn't find during execution
	namespace:  "test"
	kubeconfig: kubeconfigFile.contents
}
