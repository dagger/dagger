package main

import (
	cdk "github.com/aws/aws-cdk-go/awscdk/v2"
	ec2 "github.com/aws/aws-cdk-go/awscdk/v2/awsec2"
	ecs "github.com/aws/aws-cdk-go/awscdk/v2/awsecs"
	ecs_patterns "github.com/aws/aws-cdk-go/awscdk/v2/awsecspatterns"
	iam "github.com/aws/aws-cdk-go/awscdk/v2/awsiam"
	"github.com/aws/constructs-go/constructs/v10"
	"github.com/aws/jsii-runtime-go"
)

func NewECSStack(scope constructs.Construct, id string) cdk.Stack {
	stack := cdk.NewStack(scope, &id, &cdk.StackProps{
		Description: jsii.String("ECS/Fargate stack for dagger/CDK demo"),
	},
	)

	// ContainerImage is passed as a stack parameter
	containerImage := cdk.NewCfnParameter(stack, jsii.String("ContainerImage"), &cdk.CfnParameterProps{
		Type:    jsii.String("String"),
		Default: jsii.String("amazon/amazon-ecs-sample"),
	})

	// Create VPC and Cluster
	vpc := ec2.NewVpc(stack, jsii.String("ALBFargoVpc"), &ec2.VpcProps{
		MaxAzs: jsii.Number(2),
	})

	// Create ECS Cluster
	cluster := ecs.NewCluster(stack, jsii.String("ALBFargoECSCluster"), &ecs.ClusterProps{
		Vpc: vpc,
	})

	// Attach a Managed Policy to allow basic operations (like ECR::GetAuthorizationToken)
	role := iam.NewRole(stack, jsii.String("FargateContainerRole"), &iam.RoleProps{
		AssumedBy: iam.NewServicePrincipal(jsii.String("ecs-tasks.amazonaws.com"), &iam.ServicePrincipalOpts{}),
	})
	role.AddManagedPolicy(iam.ManagedPolicy_FromAwsManagedPolicyName(jsii.String("service-role/AmazonECSTaskExecutionRolePolicy")))

	// Create ECS Service
	res := ecs_patterns.NewApplicationLoadBalancedFargateService(stack, jsii.String("ALBFargoService"), &ecs_patterns.ApplicationLoadBalancedFargateServiceProps{
		Cluster: cluster,
		TaskImageOptions: &ecs_patterns.ApplicationLoadBalancedTaskImageOptions{
			Image:         ecs.ContainerImage_FromRegistry(containerImage.ValueAsString(), &ecs.RepositoryImageProps{}),
			ExecutionRole: role,
		},
	})

	// Output the ALB DNS
	cdk.NewCfnOutput(stack, jsii.String("LoadBalancerDNS"), &cdk.CfnOutputProps{Value: res.LoadBalancer().LoadBalancerDnsName()})

	return stack
}
