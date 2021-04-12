package main

import (
	"dagger.io/aws"
)

infra: {
	// AWS auth & default region
	awsConfig: aws.#Config

	// VPC Id
	vpcId: string

	// ECR Image repository
	ecrRepository: string

	// ECS cluster name
	ecsClusterName: string

	// Execution Role ARN used for all tasks running on the cluster
	ecsTaskRoleArn?: string

	// ELB listener ARN
	elbListenerArn: string

	// Secret ARN for the admin password of the RDS Instance
	rdsAdminSecretArn: string

	// ARN of the RDS Instance
	rdsInstanceArn: string
}
