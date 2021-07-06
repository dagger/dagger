package kustomize

import (
	"encoding/yaml"
	"alpha.dagger.io/dagger"
)

TestKustomize: {
	testdata: dagger.#Artifact

	// Run Kustomize
	manifest: #Kustomize & {
		source:        testdata
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

	// Verify kustomization generation
	test: #VerifyKustomize & {
		source: manifest
	}
}
