package main

import (
	"dagger.io/dagger"
	"dagger.io/aws"
	"dagger.io/aws/ecs"
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
}

// Backend configuration
backend: {
	// Source code to build this container
	source: dagger.#Artifact

	// Container environment variables
	environment: [string]: string

	// Public hostname (need to match the master domain configures on the loadbalancer)
	hostname: string

	// Container configuration
	container: {
		// Desired number of running containers
		desiredCount: *1 | int
		// Time to wait for the HTTP timeout to complete
		healthCheckTimeout: *10 | int
		// HTTP Path to perform the healthcheck request (HTTP Get)
		healthCheckPath: *"/" | string
		// Number of times the health check needs to fail before recycling the container
		healthCheckUnhealthyThreshold: *2 | int
		// Port used by the process inside the container
		port: *80 | int
		// Memory to allocate
		memory: *1024 | int
		// Override the default container command
		command: [...string]
		// Custom dockerfile path
		dockerfilePath: *"" | string
		// docker build args
		dockerBuildArgs: [string]: string
	}

	// Init container runs only once when the main container starts
	initContainer: {
		command: [...string]
		environment: [string]: string
	}
}

// Backend deployment logic
backend: {
	let slug = name

	// Docker image built from source, pushed to ECR
	image: #ECRImage & {
		source:     source
		repository: infra.ecrRepository
		tag:        slug
		awsConfig:  infra.awsConfig
		if backend.container.dockerfilePath != "" {
			dockerfilePath: backend.container.dockerfilePath
		}
		buildArgs: backend.container.dockerBuildArgs
	}

	// Creates an ECS Task + Service + deploy via Cloudformation
	app: #ECSApp & {
		awsConfig:      infra.awsConfig
		"slug":         slug
		clusterName:    infra.ecsClusterName
		vpcId:          infra.vpcId
		elbListenerArn: infra.elbListenerArn
		if infra.ecsTaskRoleArn != _|_ {
			taskRoleArn: infra.ecsTaskRoleArn
		}
		hostname: hostname
		healthCheck: {
			timeout:                 backend.container.healthCheckTimeout
			path:                    backend.container.healthCheckPath
			unhealthyThresholdCount: backend.container.healthCheckUnhealthyThreshold
		}
		desiredCount: backend.container.desiredCount
		container: {
			command:     backend.container.command
			environment: environment
			port:        backend.container.port
			memory:      backend.container.memory
			"image":     image.ref
		}
	}

	// Optional container to run one-time during the deploy (eg. db migration)
	if len(backend.initContainer.command) > 0 {
		initContainer: ecs.#RunTask & {
			config:        infra.awsConfig
			containerName: slug
			cluster:       infra.ecsClusterName
			if infra.ecsTaskRoleArn != _|_ {
				roleArn: infra.ecsTaskRoleArn
			}
			containerEnvironment: backend.initContainer.environment
			containerCommand:     backend.initContainer.command
			taskArn:              app.taskArn
		}
	}
}
