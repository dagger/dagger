package helm

import (
	"dagger.io/kubernetes/helm"
	"dagger.io/dagger"
	"dagger.io/file"
)

// We assume that a kinD cluster is running locally
// To deploy a local KinD cluster, follow this link : https://kind.sigs.k8s.io/docs/user/quick-start/
kubeconfig: dagger.#Artifact

// Retrive kubeconfig
config: file.#Read & {
	filename: "config"
	from:     kubeconfig
}

// Dagger test k8s namespace
namespace: "dagger-test"

chartName: "test-helm"

// Example of a `helm install` using a local chart
// Fill using:
//          --input-dir helmChart.chart=./testdata/mychart
TestHelmSimpleChart: {
	helm.#Chart & {
		name:        chartName
		"namespace": namespace
		kubeconfig:  config.contents
		chart:       dagger.#Artifact
	}

	verify: #VerifyHelm & {
		"chartName": chartName
	}
}

result: helmApply: TestHelmSimpleChart.verify
