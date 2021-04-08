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

// FIXME original_cue_error=kubeApply.#up.3.content: incomplete value string op=0s
// Example of a simple `kubectl apply` using a simple config
kubeApply: kubernetes.#Apply & {
	sourceInline: yaml.Marshal(kubeSrc)
	namespace:  "test"
	kubeconfig: kubeconfigFile.contents
	kNetwork: "host"
}
