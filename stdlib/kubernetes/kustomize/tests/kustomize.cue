package kustomize

import (
	"encoding/yaml"
	"dagger.io/dagger"
)

TestKustomize: {
	testdata: dagger.#Artifact

	// Run Kustomize
	kustom: #Kustomize & {
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
	verify: #VerifyKustomize & {
		source: kustom
	}
}
