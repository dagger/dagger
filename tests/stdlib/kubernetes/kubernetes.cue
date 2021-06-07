package kubernetes

import (
	"encoding/yaml"
	"dagger.io/dagger"
	"dagger.io/kubernetes"
)

// We assume that a kinD cluster is running locally
// To deploy a local KinD cluster, follow this link : https://kind.sigs.k8s.io/docs/user/quick-start/
kubeconfig: dagger.#Secret @dagger(input)

TestKubeApply: {
	random: #Random & {}

	// Pod spec
	kubeSrc: {
		apiVersion: "v1"
		kind:       "Pod"
		metadata: name: "kube-test-\(random.out)"
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
		"kubeconfig": kubeconfig
		namespace:    "dagger-test"
		manifest:     yaml.Marshal(kubeSrc)
	}

	// Verify deployment
	verify: #VerifyApply & {
		podname:   kubeSrc.metadata.name
		namespace: apply.namespace
	}
}
