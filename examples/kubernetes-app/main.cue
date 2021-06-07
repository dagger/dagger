package main

import (
	"encoding/yaml"
	"dagger.io/dagger"
	"dagger.io/aws"
	"dagger.io/aws/eks"
	"dagger.io/kubernetes"
	"dagger.io/kubernetes/helm"
)

kubeSrc: {
	apiVersion: "v1"
	kind:       "Pod"
	metadata: name: "kube-test"
	spec: {
		restartPolicy: "Never"
		containers: [{
			name:  "test"
			image: "hello-world"
		}]
	}
}

// Fill using:
//          --input-string awsConfig.accessKey=XXX
//          --input-string awsConfig.secretKey=XXX
awsConfig: aws.#Config & {
	region: *"us-east-2" | string
}

// Take the kubeconfig from the EKS cluster
cluster: eks.#KubeConfig & {
	config:      awsConfig
	clusterName: *"dagger-example-eks-cluster" | string
}

// Example of a simple `kubectl apply` using a simple config
kubeApply: kubernetes.#Resources & {
	manifest:   yaml.Marshal(kubeSrc)
	namespace:  "test"
	kubeconfig: cluster.kubeconfig
}

// Example of a `helm install` using a local chart
// Fill using:
//          --input-dir helmChart.chartSource=./testdata/mychart
helmChart: helm.#Chart & {
	name:        "test-helm"
	namespace:   "test"
	kubeconfig:  cluster.kubeconfig
	chartSource: dagger.#Artifact
}
