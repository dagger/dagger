package kubernetes

import (
	"encoding/yaml"
	"dagger.io/dagger"
	"dagger.io/dagger/op"
	"dagger.io/alpine"
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

// Generate a random number
// It trigger a "cycle error" if I put it in TestKubeApply ?!
// failed to up deployment: buildkit solve: TestKubeApply.#up: cycle error
random: {
	string
	#up: [
		op.#Load & {from: alpine.#Image},
		op.#Exec & {
			args: ["sh", "-c", "cat /dev/urandom | tr -dc 'a-z' | fold -w 10 | head -n 1 | tr -d '\n' > /rand"]
		},
		op.#Export & {
			source: "/rand"
		},
	]
}

TestKubeApply: {
	// Pod spec
	kubeSrc: {
		apiVersion: "v1"
		kind:       "Pod"
		metadata: name: "kube-test-\(random)"
		spec: {
			restartPolicy: "Never"
			containers: [{
				name:  "test"
				image: "hello-world"
			}]
		}
	}

	// Apply deployment
	apply: kubernetes.#Apply & {
		kubeconfig:   config.contents
		namespace:    "dagger-test"
		sourceInline: yaml.Marshal(kubeSrc)
	}

	// Verify deployment
	verify: #VerifyApply & {
		podname:   kubeSrc.metadata.name
		namespace: apply.namespace
	}
}
