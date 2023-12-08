package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"dagger.io/dagger"
)

func main() {

	// check for required variables in host environment
	vars := []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_DEFAULT_REGION"}
	for _, v := range vars {
		if os.Getenv(v) == "" {
			log.Fatalf("Environment variable %s is not set", v)
		}
	}

	// initialize Dagger client
	ctx := context.Background()
	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		panic(err)
	}
	defer client.Close()

	// set AWS credentials as client secrets
	awsAccessKeyId := client.SetSecret("awsAccessKeyId", os.Getenv("AWS_ACCESS_KEY_ID"))
	awsSecretAccessKey := client.SetSecret("awsSecretAccessKey", os.Getenv("AWS_SECRET_ACCESS_KEY"))

	awsRegion := os.Getenv("AWS_DEFAULT_REGION")

	// get reference to function directory
	lambdaDir := client.Host().Directory(".", dagger.HostDirectoryOpts{
		Exclude: []string{"ci"},
	})

	// use a node:18-alpine container
	// mount the function directory
	// at /src in the container
	// install application dependencies
	// create zip archive
	build := client.Container().
		From("golang:1.20-alpine").
		WithExec([]string{"apk", "add", "zip"}).
		WithDirectory("/src", lambdaDir).
		WithWorkdir("/src").
		WithEnvVariable("GOOS", "linux").
		WithEnvVariable("GOARCH", "amd64").
		WithEnvVariable("CGO_ENABLED", "0").
		WithExec([]string{"go", "build", "-o", "lambda", "lambda.go"}).
		WithExec([]string{"zip", "function.zip", "lambda"})

	// use an AWS CLI container
	// set AWS credentials and configuration
	// as container environment variables
	aws := client.Container().
		From("amazon/aws-cli:2.11.22").
		WithSecretVariable("AWS_ACCESS_KEY_ID", awsAccessKeyId).
		WithSecretVariable("AWS_SECRET_ACCESS_KEY", awsSecretAccessKey).
		WithEnvVariable("AWS_DEFAULT_REGION", awsRegion)

	// add zip archive to AWS CLI container
	// use CLI commands to deploy new zip archive
	// and get function URL
	// parse response and print URL
	response, err := aws.
		WithFile("/tmp/function.zip", build.File("/src/function.zip")).
		WithExec([]string{"lambda", "update-function-code", "--function-name", "myFunction", "--zip-file", "fileb:///tmp/function.zip"}).
		WithExec([]string{"lambda", "get-function-url-config", "--function-name", "myFunction"}).
		Stdout(ctx)
	if err != nil {
		panic(err)
	}

	var data struct {
		FunctionUrl string
	}

	err = json.Unmarshal([]byte(response), &data)
	if err != nil {
		panic(err)
	}

	fmt.Printf("Function updated at: %s\n", data.FunctionUrl)
}
