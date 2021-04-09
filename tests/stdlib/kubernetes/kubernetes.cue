package kubernetes

import (
	"encoding/yaml"
	"dagger.io/dagger"
	"dagger.io/file"
	"dagger.io/kubernetes"
)

// We assume that a kinD cluster is running locally
// To deploy a local KinD cluster, follow this link : https://kind.sigs.k8s.io/docs/user/quick-start/
kubeconfig: dagger.#Artifact

// Retrive kubeconfig
config: file.#Read & {
	filename: "config"
	from:     kubeconfig
}

// Pod uid
// Can be better if it's a random id in real test
uid: string

kubeSrc: {
	apiVersion: "v1"
	kind:       "Pod"
	metadata: name: "kube-test-\(uid)"
	spec: {
		restartPolicy: "Never"
		containers: [{
			name:  "test"
			image: "hello-world"
		}]
	}
}

// Dagger test k8s namespace
namespace: "dagger-test"

TestKubeApply: {
	kubernetes.#Apply & {
		kubeconfig:   config.contents
		"namespace":  namespace
		sourceInline: yaml.Marshal(kubeSrc)
	}

	verify: #VerifyApply & {
		podname: "kube-test-\(uid)"
	}
}

result: kubeApply: TestKubeApply.verify
