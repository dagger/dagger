package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"strings"

	"dagger.io/dagger"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudformation"
	"github.com/aws/aws-sdk-go-v2/service/cloudformation/types"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	"github.com/aws/jsii-runtime-go"
)

type AWSClient struct {
	region string
	cCfn   *cloudformation.Client
	cEcr   *ecr.Client
}

func NewAWSClient(ctx context.Context, region string) (*AWSClient, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, err
	}

	cfg.Region = region
	client := &AWSClient{
		region: region,
		cCfn:   cloudformation.NewFromConfig(cfg),
		cEcr:   ecr.NewFromConfig(cfg),
	}

	return client, nil
}

func (c *AWSClient) GetCfnStackOutputs(ctx context.Context, stackName string) (map[string]string, error) {
	out, err := c.cCfn.DescribeStacks(ctx, &cloudformation.DescribeStacksInput{
		StackName: jsii.String(stackName),
	})

	if err != nil {
		return nil, err
	}

	if len(out.Stacks) < 1 {
		return nil, fmt.Errorf("cannot DescribeStack name %q", stackName)
	}

	stack := out.Stacks[0]
	// status := string(stack.StackStatus)

	return FormatStackOutputs(stack.Outputs), nil
}

func (c *AWSClient) GetECRAuthorizationToken(ctx context.Context) (string, error) {
	log.Printf("ECR GetAuthorizationToken for region %q", c.region)
	out, err := c.cEcr.GetAuthorizationToken(ctx, &ecr.GetAuthorizationTokenInput{})
	if err != nil {
		return "", err
	}

	if len(out.AuthorizationData) < 1 {
		return "", fmt.Errorf("GetECRAuthorizationToken returned empty AuthorizationData")
	}

	authToken := *out.AuthorizationData[0].AuthorizationToken
	return authToken, nil
}

// GetECRUsernamePassword fetches ECR auth token and converts it to username / password
func (c *AWSClient) GetECRUsernamePassword(ctx context.Context) (string, string, error) {
	token, err := c.GetECRAuthorizationToken(ctx)
	if err != nil {
		return "", "", err
	}

	decoded, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		return "", "", err
	}

	split := strings.SplitN(string(decoded), ":", 2)
	if len(split) < 1 {
		return "", "", fmt.Errorf("invalid base64 decoded data")
	}

	return split[0], split[1], nil
}

// FormatStackOutputs converts stack outputs into a map of string for easy printing
func FormatStackOutputs(outputs []types.Output) map[string]string {
	outs := map[string]string{}

	for _, o := range outputs {
		outs[*o.OutputKey] = *o.OutputValue
	}

	return outs
}

// cdkDeployStack deploys a CloudFormation stack via the CDK cli
func (c *AWSClient) cdkDeployStack(ctx context.Context, client *dagger.Client, stackName string, stackParameters map[string]string) (map[string]string, error) {
	cdkCode := client.Host().Directory("./infra", dagger.HostDirectoryOpts{
		Exclude: []string{"cdk.out/", "infra"},
	})

	awsConfig := client.Host().Directory(os.ExpandEnv("${HOME}/.aws"))

	cdkCommand := []string{"cdk", "deploy", "--require-approval=never", stackName}
	// Append the stack parameters to the CLI, if there is any
	for k, v := range stackParameters {
		cdkCommand = append(cdkCommand, "--parameters", fmt.Sprintf("%s=%s", k, v))
	}

	exitCode, err := client.Container().From("samalba/aws-cdk:2.65.0").
		WithEnvVariable("AWS_REGION", c.region).
		WithEnvVariable("AWS_DEFAULT_REGION", c.region).
		WithMountedDirectory("/opt/app", cdkCode).
		WithMountedDirectory("/root/.aws", awsConfig).
		WithExec(cdkCommand).
		ExitCode(ctx)

	if err != nil {
		return nil, err
	}

	if exitCode != 0 {
		return nil, fmt.Errorf("cdk deploy exited with code %d", exitCode)
	}

	outputs, err := c.GetCfnStackOutputs(ctx, stackName)
	if err != nil {
		return nil, err
	}

	return outputs, nil
}
