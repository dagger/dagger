package main

import (
	"dagger.io/dagger"
	"dagger.io/aws/ecs"
	"dagger.io/git"
)

// Backend configuration
backend: {

	// Source code to build this container
	source: git.#Repository | dagger.#Artifact @dagger(input)

	// Container environment variables
	environment: {
		[string]: string
	} @dagger(input)

	// Public hostname (need to match the master domain configures on the loadbalancer)
	hostname: string @dagger(input)

	// Container configuration
	container: {
		// Desired number of running containers
		desiredCount: *1 | int @dagger(input)
		// Time to wait for the HTTP timeout to complete
		healthCheckTimeout: *10 | int @dagger(input)
		// HTTP Path to perform the healthcheck request (HTTP Get)
		healthCheckPath: *"/" | string @dagger(input)
		// Number of times the health check needs to fail before recycling the container
		healthCheckUnhealthyThreshold: *2 | int @dagger(input)
		// Port used by the process inside the container
		port: *80 | int @dagger(input)
		// Memory to allocate
		memory: *1024 | int @dagger(input)
		// Override the default container command
		command: [...string] @dagger(input)
		// Custom dockerfile path
		dockerfilePath: *"" | string @dagger(input)
		// docker build args
		dockerBuildArgs: {
			[string]: string
		} @dagger(input)
	}

	// Init container runs only once when the main container starts
	initContainer: {
		command: [...string] @dagger(input)
		environment: {
			[string]: string
		} @dagger(input)
	}
}

// Backend deployment logic
backend: {
	let slug = name

	// Docker image built from source, pushed to ECR
	image: #ECRImage & {
		source:     backend.source
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
		hostname: backend.hostname
		healthCheck: {
			timeout:                 backend.container.healthCheckTimeout
			path:                    backend.container.healthCheckPath
			unhealthyThresholdCount: backend.container.healthCheckUnhealthyThreshold
		}
		desiredCount: backend.container.desiredCount
		container: {
			command:     backend.container.command
			environment: backend.environment
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
