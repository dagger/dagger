import * as pulumi from "@pulumi/pulumi";
import * as aws from "@pulumi/aws";
import * as awsx from "@pulumi/awsx";

const imageName = new pulumi.Config().require("imageName");

const vpc = new awsx.ec2.Vpc("vpc", {
  natGateways: {
    strategy: awsx.ec2.NatGatewayStrategy.Single,
  }
});

const cluster = new aws.ecs.Cluster("cluster");

const group = new aws.ec2.SecurityGroup("web-secgrp", {
  vpcId: vpc.vpcId,
  description: "Enable HTTP access",
  ingress: [{
    protocol: "tcp",
    fromPort: 80,
    toPort: 80,
    cidrBlocks: ["0.0.0.0/0"],
  }],
  egress: [{
    protocol: "-1",
    fromPort: 0,
    toPort: 0,
    cidrBlocks: ["0.0.0.0/0"],
  }],
});

const alb = new aws.lb.LoadBalancer("app-lb", {
  securityGroups: [group.id],
  subnets: vpc.publicSubnetIds,
});

const targetGroup = new aws.lb.TargetGroup("app-tg", {
  port: 80,
  protocol: "HTTP",
  targetType: "ip",
  vpcId: vpc.vpcId,
});

const listener = new aws.lb.Listener("web", {
  loadBalancerArn: alb.arn,
  port: 80,
  defaultActions: [{
    type: "forward",
    targetGroupArn: targetGroup.arn,
  }],
});

const role = new aws.iam.Role("task-exec-role", {
  assumeRolePolicy: JSON.stringify({
    Version: "2008-10-17",
    Statement: [{
      Action: "sts:AssumeRole",
      Principal: {
        Service: "ecs-tasks.amazonaws.com",
      },
      Effect: "Allow",
      Sid: "",
    }],
  }),
});

new aws.iam.RolePolicyAttachment("task-exec-policy", {
  role: role.name,
  policyArn: aws.iam.ManagedPolicy.AmazonECSTaskExecutionRolePolicy,
});

const taskDefinition = new aws.ecs.TaskDefinition("app-task", {
  family: "fargate-task-definition",
  cpu: "256",
  memory: "512",
  networkMode: "awsvpc",
  requiresCompatibilities: ["FARGATE"],
  executionRoleArn: role.arn,
  containerDefinitions: JSON.stringify([{
    name: "my-app",
    image: imageName,
    portMappings: [{
      containerPort: 80,
      hostPort: 80,
      protocol: "tcp",
    }],
  }]),
});

const service = new aws.ecs.Service("app-svc", {
  cluster: cluster.arn,
  desiredCount: 1,
  launchType: "FARGATE",
  taskDefinition: taskDefinition.arn,
  networkConfiguration: {
    assignPublicIp: true,
    subnets: vpc.privateSubnetIds,
    securityGroups: [group.id],
  },
  loadBalancers: [{
    targetGroupArn: targetGroup.arn,
    containerName: "my-app",
    containerPort: 80,
  }],
});

export const serviceUrl = pulumi.interpolate`http://${alb.dnsName}`;
