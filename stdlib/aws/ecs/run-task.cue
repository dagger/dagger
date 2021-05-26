package ecs

import (
	"dagger.io/aws"
)

// RunTask implement ecs run-task
#RunTask: {

	// AWS Config
	config: aws.#Config

	// ECS cluster name
	cluster: string @dagger(input)

	// Arn of the task to run
	taskArn: string @dagger(input)

	// Environment variables of the task
	containerEnvironment: [string]: string @dagger(input)

	// Container name
	containerName: string @dagger(input)

	// Container command to give
	containerCommand: [...string] @dagger(input)

	// Task role ARN
	roleArn: string | *"" @dagger(input)

	containerOverrides: {
		containerOverrides: [{
			name: containerName
			if len(containerCommand) > 0 {
				command: containerCommand
			}
			if len(containerEnvironment) > 0 {
				environment: [ for k, v in containerEnvironment {
					name:  k
					value: v
				}]
			}
		}]
		if roleArn != "" {
			taskRoleArn: roleArn
		}
	}

	aws.#Script & {
		"config": config
		export:   "/out"
		files: {
			"/inputs/cluster":             cluster
			"/inputs/task_arn":            taskArn
			"/inputs/container_overrides": containerOverrides
		}
		code: #"""
			cat /inputs/container_overrides | jq

			aws ecs run-task \
				--cluster "$(cat /inputs/cluster)" \
				--task-definition "$(cat /inputs/task_arn)" \
				--overrides "$(cat /inputs/container_overrides)" \
				> /out
			"""#
	}
}
