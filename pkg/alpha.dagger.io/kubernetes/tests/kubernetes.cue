package kubernetes

import (
	"encoding/yaml"
	"alpha.dagger.io/random"
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
	resources: #Resources & {
		kubeconfig: TestKubeconfig
		namespace:  "dagger-test"
		manifest:   yaml.Marshal(kubeSrc)
	}

	// Verify deployment
	verify: #VerifyApply & {
		podname:   kubeSrc.metadata.name
		namespace: resources.namespace
	}
}

TestLinkApply: {
	// Podname from hello-world-pod
	_podname: "hello-world"

	// Apply deployment
	resources: #Resources & {
		kubeconfig: TestKubeconfig
		namespace:  "dagger-test"
		url:        "https://gist.githubusercontent.com/grouville/04402633618f3289a633f652e9e4412c/raw/293fa6197b78ba3fad7200fa74b52c62ec8e6703/hello-world-pod.yaml"
	}

	// Verify deployment
	verify: #VerifyApply & {
		podname:   _podname
		namespace: resources.namespace
	}
}
