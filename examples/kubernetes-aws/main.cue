package main

import (
	"dagger.io/aws"
	"dagger.io/aws/eks"
)

// AWS account: credentials and region
awsConfig: aws.#Config & {
	region: *"us-east-2" | string
}

// Auto-provision an EKS cluster:
// - VPC, Nat Gateways, Subnets, Security Group
// - EKS Cluster
// - Instance Node Group: auto-scaling-group, ec2 instances, etc...
// base config can be changed (number of EC2 instances, types, etc...)
infra: #Infrastructure & {
	"awsConfig":            awsConfig
	namePrefix:             "dagger-example-"
	workerNodeCapacity:     int | *1
	workerNodeInstanceType: "t3.small"
}

// Client configuration for kubectl
kubeconfig: eks.#KubeConfig & {
	config:      awsConfig
	clusterName: infra.clusterName
}
