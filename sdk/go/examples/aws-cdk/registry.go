package main

import (
	"context"

	"dagger.io/dagger"
)

type RegistryInfo struct {
	uri      string
	username string
	password string
}

// initRegistry creates and/or authenticate with an ECR repository
func initRegistry(ctx context.Context, client *dagger.Client, awsClient *AWSClient) *RegistryInfo {
	outputs, err := awsClient.cdkDeployStack(ctx, client, "DaggerDemoECRStack", nil)
	if err != nil {
		panic(err)
	}

	repoUri := outputs["RepositoryUri"]

	username, password, err := awsClient.GetECRUsernamePassword(ctx)
	if err != nil {
		panic(err)
	}

	return &RegistryInfo{repoUri, username, password}
}
