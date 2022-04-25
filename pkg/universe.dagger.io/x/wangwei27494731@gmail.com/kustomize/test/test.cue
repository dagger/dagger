package kustomize

import (
	"dagger.io/dagger"
	"universe.dagger.io/x/wangwei27494731@gmail.com/kustomize"
	"encoding/yaml"
)

dagger.#Plan & {
	client: {
		env: KUBECONFIG: string
		commands: kubeconfig: {
			name: "cat"
			args: ["\(env.KUBECONFIG)"]
			stdout: dagger.#Secret
		}
	}
	actions: test: {
		// Run Kustomize
		kustom: kustomize.#Kustomize & {
			source:        "./testdata"
			kubeconfig:    client.commands.kubeconfig.stdout
			kustomization: yaml.Marshal({
				resources: ["deployment.yaml", "pod.yaml"]
				images: [{
					name:   "nginx"
					newTag: "v1"
				}]
				replicas: [{
					name:  "nginx-deployment"
					count: 2
				}]
			})
		}
	}
}
