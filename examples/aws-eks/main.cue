package main

import (
	"dagger.io/aws"
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
}
