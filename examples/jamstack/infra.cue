package main

import (
	"dagger.io/aws"
	"dagger.io/netlify"
)

infra: {
	// AWS auth & default region
	awsConfig: aws.#Config

	// VPC Id
	vpcId: string @dagger(input)

	// ECR Image repository
	ecrRepository: string @dagger(input)

	// ECS cluster name
	ecsClusterName: string @dagger(input)

	// Execution Role ARN used for all tasks running on the cluster
	ecsTaskRoleArn?: string @dagger(input)

	// ELB listener ARN
	elbListenerArn: string @dagger(input)

	// Secret ARN for the admin password of the RDS Instance
	rdsAdminSecretArn: string @dagger(input)

	// ARN of the RDS Instance
	rdsInstanceArn: string @dagger(input)

	// Netlify credentials
	netlifyAccount: netlify.#Account @dagger(input)
}
