package main

import (
	"encoding/yaml"
	"dagger.io/kubernetes"
	"dagger.io/random"
)

// We assume that a kinD cluster is running locally
// To deploy a local KinD cluster, follow this link : https://kind.sigs.k8s.io/docs/user/quick-start/
TestKubeconfig: string @dagger(input)

TestKubeApply: {
	suffix: random.#String & {
		seed: ""
	}

	// Pod spec
	kubeSrc: {
		apiVersion: "v1"
		kind:       "Pod"
		metadata: name: "kube-test-\(suffix.out)"
		spec: {
			restartPolicy: "Never"
			containers: [{
				name:  "test"
				image: "hello-world"
			}]
		}
	}

	// Apply deployment
	apply: kubernetes.#Resources & {
		kubeconfig: TestKubeconfig
		namespace:  "dagger-test"
		manifest:   yaml.Marshal(kubeSrc)
	}

	// Verify deployment
	verify: #VerifyApply & {
		podname:   kubeSrc.metadata.name
		namespace: apply.namespace
	}
}
