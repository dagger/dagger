// AWS Elastic Container Service (ECS)
package ecs

import (
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/aws"
)

// Task implements ecs run-task for running a single container on ECS
#Task: {

	// AWS Config
	config: aws.#Config

	// ECS cluster name
	cluster: string & dagger.#Input

	// Arn of the task to run
	taskArn: string & dagger.#Input

	// Environment variables of the task
	containerEnvironment: {
		[string]: string
	} & dagger.#Input

	// Container name
	containerName: string & dagger.#Input

	// Container command to give
	containerCommand: [...string] & dagger.#Input

	// Task role ARN
	roleArn: *"" | string & dagger.#Input

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
