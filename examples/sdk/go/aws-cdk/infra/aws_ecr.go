package main

import (
	"fmt"

	cdk "github.com/aws/aws-cdk-go/awscdk/v2"
	ecr "github.com/aws/aws-cdk-go/awscdk/v2/awsecr"
	"github.com/aws/constructs-go/constructs/v10"

	"github.com/aws/jsii-runtime-go"
)

func NewECRStack(scope constructs.Construct, id string, repositoryName string) cdk.Stack {
	stack := cdk.NewStack(scope, &id, &cdk.StackProps{
		Description: jsii.String(fmt.Sprintf("ECR stack for repository %s", repositoryName)),
	})

	ecrRepo := ecr.NewRepository(stack, jsii.String(repositoryName), &ecr.RepositoryProps{
		RepositoryName: jsii.String(repositoryName),
	})

	cdk.NewCfnOutput(stack, jsii.String("RepositoryUri"), &cdk.CfnOutputProps{Value: ecrRepo.RepositoryUri()})

	return stack
}
