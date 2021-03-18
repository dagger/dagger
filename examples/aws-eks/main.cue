package main

import (
	"dagger.io/aws"
	"dagger.io/aws/eks"
)

// Fill using:
//          --input-string awsConfig.accessKey=XXX
//          --input-string awsConfig.secretKey=XXX
awsConfig: aws.#Config & {
	region: *"us-east-2" | string
}

// Auto-provision an EKS cluster:
// - VPC, Nat Gateways, Subnets, Security Group
// - EKS Cluster
// - Instance Node Group: auto-scaling-group, ec2 instances, etc...
// base config can be changed (number of EC2 instances, types, etc...)
infra: #Infrastructure & {
	"awsConfig": awsConfig
	namePrefix:  "dagger-example-"
	// Cluster size is 1 for the example purpose
	workerNodeCapacity:     1
	workerNodeInstanceType: "t3.small"
}

kubeconfig: eks.#KubeConfig & {
	config:      awsConfig
	clusterName: infra.clusterName
}
