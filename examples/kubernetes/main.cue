package main

import (
    "encoding/yaml"
    "dagger.io/aws"
    "dagger.io/aws/eks"
    "dagger.io/kubernetes"
)

kubeSrc: {
    apiVersion: "v1"
    kind:       "Pod"
    metadata: {
        name: "kube-test"
    }
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

cluster: eks.#KubeConfig & {
	config:      awsConfig
	clusterName: *"dagger-example-eks-cluster" | string
}

apply: kubernetes.#Apply & {
    source: yaml.Marshal(kubeSrc)
    namespace: "test"
    kubeconfig: cluster.kubeconfig
}
