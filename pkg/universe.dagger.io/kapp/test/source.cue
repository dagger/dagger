package kapp

import (
	"encoding/yaml"
	"alpha.dagger.io/random"
	"github.com/renuy/kapp"
)

// We assume that a kinD cluster is running locally
// To deploy a local KinD cluster, follow this link : https://kind.sigs.k8s.io/docs/user/quick-start/
TestKubeconfig: string @dagger(input)

TestKappDeploy: {
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
	deploy: kapp.#Deploy & {
		app: "dagger-test"
		manifest:   yaml.Marshal(kubeSrc)
		kubeconfig: TestKubeconfig
	}

	// Verify deployment
	verify: #VerifyApply & {
		namespace: deploy.namespace
	}

}

