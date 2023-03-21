package main

import (
	"context"
	"fmt"
	"os"

	"dagger.io/dagger"
	// "github.com/aws/aws-cdk-go/awscdk/v2/awssqs"
)

// build reads the source code, run the test and build the app and publish it to a container registry
func build(ctx context.Context, client *dagger.Client, registry *RegistryInfo) (string, error) {
	nodeCache := client.CacheVolume("node")

	// // Read the source code from local directory
	// sourceDir := client.Host().Directory("./app", dagger.HostDirectoryOpts{
	// 	Exclude: []string{"node_modules/"},
	// })

	// Read the source code from a remote git repository
	sourceDir := client.Git("https://github.com/dagger/hello-dagger.git").
		Commit("5343dfee12cfc59013a51886388a7cacee3f16b9").
		Tree().
		Directory(".")

	source := client.Container().
		From("node:16").
		WithMountedDirectory("/src", sourceDir).
		WithMountedCache("/src/node_modules", nodeCache)

	runner := source.WithWorkdir("/src").
		WithExec([]string{"npm", "install"})

	test := runner.WithExec([]string{"npm", "test", "--", "--watchAll=false"})

	buildDir := test.WithExec([]string{"npm", "run", "build"}).
		Directory("./build")

	// FIXME: This is a workaround until there is a better way to create a secret from the API
	registrySecret := client.Container().WithNewFile("/secret", dagger.ContainerWithNewFileOpts{
		Contents:    registry.password,
		Permissions: 0o400,
	}).File("/secret").Secret()

	// Explicitly build for "linux/amd64" to match the target (container on Fargate)
	return client.Container(dagger.ContainerOpts{Platform: "linux/amd64"}).
		From("nginx").
		WithDirectory("/usr/share/nginx/html", buildDir).
		WithRegistryAuth("125635003186.dkr.ecr.us-west-1.amazonaws.com", registry.username, registrySecret).
		Publish(ctx, registry.uri)
}

// deployToECS deploys a container image to the ECS cluster
func deployToECS(ctx context.Context, client *dagger.Client, awsClient *AWSClient, containerImage string) string {
	stackParameters := map[string]string{
		"ContainerImage": containerImage,
	}

	outputs, err := awsClient.cdkDeployStack(ctx, client, "DaggerDemoECSStack", stackParameters)
	if err != nil {
		panic(err)
	}

	return outputs["LoadBalancerDNS"]
}

func main() {
	ctx := context.Background()

	// initialize Dagger client
	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stdout))
	if err != nil {
		panic(err)
	}
	defer client.Close()

	// initialize AWS client
	awsClient, err := NewAWSClient(ctx, "us-west-1")
	if err != nil {
		panic(err)
	}

	// init the ECR Registry using the AWS CDK
	registry := initRegistry(ctx, client, awsClient)
	imageRef, err := build(ctx, client, registry)
	if err != nil {
		panic(err)
	}

	fmt.Println("Published image to", imageRef)

	// init and deploy to ECS using the AWS CDK
	publicDNS := deployToECS(ctx, client, awsClient, imageRef)

	fmt.Printf("Deployed to http://%s/\n", publicDNS)
}
