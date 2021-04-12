package main

import (
	"encoding/json"

	"dagger.io/aws"
	"dagger.io/aws/elb"
	"dagger.io/aws/cloudformation"
)

#ECSApp: {
	awsConfig:      aws.#Config
	slug:           string
	clusterName:    string
	vpcId:          string
	elbListenerArn: string
	taskRoleArn:    *"" | string
	hostname:       string
	healthCheck: {
		timeout:                 *10 | int
		path:                    *"/" | string
		unhealthyThresholdCount: *2 | int
	}
	desiredCount: int
	container: {
		command: [...string]
		environment: [string]: string
		port:   *80 | int
		cpu:    *256 | int
		memory: *1024 | int
		image:  string
	}

	taskArn: cfnStack.outputs.TaskArn

	elbRulePriority: elb.#RandomRulePriority & {
		config:      awsConfig
		listenerArn: elbListenerArn
		vhost:       hostname
	}

	cfnStack: cloudformation.#Stack & {
		config:    awsConfig
		stackName: slug
		onFailure: "DO_NOTHING"
		parameters: {
			ELBRulePriority: elbRulePriority.out
			ImageRef:        container.image
			ELBListenerArn:  elbListenerArn
		}
		source: json.Marshal(template)
	}

	template: {
		AWSTemplateFormatVersion: "2010-09-09"
		Description:              "Dagger deployed app"
		Parameters: {
			ELBRulePriority: Type: "Number"
			ImageRef: Type:        "String"
			ELBListenerArn: Type:  "String"
		}
		Resources: {
			ECSTaskDefinition: {
				Type: "AWS::ECS::TaskDefinition"
				Properties: {
					Cpu:    "\(container.cpu)"
					Memory: "\(container.memory)"
					if taskRoleArn != "" {
						TaskRoleArn: taskRoleArn
					}
					NetworkMode: "bridge"
					ContainerDefinitions: [{
						if len(container.command) > 0 {
							Command: container.command
						}
						Name: slug
						Image: Ref: "ImageRef"
						Essential: true
						Environment: [ for k, v in container.environment {
							Name:  k
							Value: v
						}]
						PortMappings: [{
							ContainerPort: container.port
						}]
						StopTimeout: 5
						LogConfiguration: {
							LogDriver: "awslogs"
							Options: {
								"awslogs-group": "bl/provider/ecs/\(clusterName)"
								"awslogs-region": Ref: "AWS::Region"
								"awslogs-create-group":  "true"
								"awslogs-stream-prefix": slug
							}
						}
					}]
				}
			}
			ECSListenerRule: {
				Type: "AWS::ElasticLoadBalancingV2::ListenerRule"
				Properties: {
					ListenerArn: Ref: "ELBListenerArn"
					Priority: Ref:    "ELBRulePriority"
					Conditions: [{
						Field: "host-header"
						Values: [hostname]}]
					Actions: [{
						Type: "forward"
						TargetGroupArn: Ref: "ECSTargetGroup"
					}]}}
			ECSTargetGroup: {
				Type: "AWS::ElasticLoadBalancingV2::TargetGroup"
				Properties: {
					Protocol:                   "HTTP"
					VpcId:                      vpcId
					Port:                       80
					HealthCheckPath:            healthCheck.path
					UnhealthyThresholdCount:    healthCheck.unhealthyThresholdCount
					HealthCheckTimeoutSeconds:  healthCheck.timeout
					HealthCheckIntervalSeconds: healthCheck.timeout + 1
					HealthyThresholdCount:      3
					TargetGroupAttributes: [{
						Value: "10"
						Key:   "deregistration_delay.timeout_seconds"
					}]}}
			ECSService: {
				Type: "AWS::ECS::Service"
				Properties: {
					Cluster:      clusterName
					DesiredCount: desiredCount
					LaunchType:   "EC2"
					LoadBalancers: [{
						ContainerPort: container.port
						TargetGroupArn: Ref: "ECSTargetGroup"
						ContainerName: slug
					}]
					ServiceName: slug
					TaskDefinition: Ref: "ECSTaskDefinition"
					DeploymentConfiguration: {
						DeploymentCircuitBreaker: {
							Enable:   true
							Rollback: true
						}
						MaximumPercent:        200
						MinimumHealthyPercent: 100
					}}
				DependsOn: "ECSListenerRule"
			}
		}
		Outputs: TaskArn: Value: Ref: "ECSTaskDefinition"
	}
}
