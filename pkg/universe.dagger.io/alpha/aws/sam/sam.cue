package sam

import (
	"dagger.io/dagger"
	"universe.dagger.io/docker"
)

// Working directory in Sam image
_destination: "/var/task"

// AWS Sam image
// https://hub.docker.com/r/amazon/aws-sam-cli-build-image-provided/tags
#Image: docker.#Pull & {
	source: "index.docker.io/amazon/aws-sam-cli-build-image-provided@sha256:56ba3e64d305d11379dc1bc0196d9b9b411c0b7dacb3f5dd6ecdffc55b6f2958"
}

// DEPRECATED: Use sam.#Image instead
#DefaultImage: #Image

// Executes the sam command
#Sam: {
	// Default AWS Sam Docker image
	_defaultImage: #Image
	input:         docker.#Image | *_defaultImage.output

	// Sam configuration
	config: #Config

	docker.#Run & {
		if (config.ciKey != _|_) {
			env: {
				DOCKER_HOST:           config.host
				DOCKER_TLS_VERIFY:     "1"
				DOCKER_CERT_PATH:      "/certs/client"
				AWS_ACCESS_KEY_ID:     config.accessKey
				AWS_SECRET_ACCESS_KEY: config.secretKey
			}
			mounts: certs: {
				dest:     "/certs/client"
				contents: config.certs
			}
		}

		if (config.ciKey == _|_) {
			mounts: dkr: {
				dest:     "/var/run/docker.sock"
				contents: config.clientSocket
			}
			env: {
				AWS_ACCESS_KEY_ID:     config.accessKey
				AWS_SECRET_ACCESS_KEY: config.secretKey
			}
		}

		workdir: _destination
		command: name: "sam"
	}
}

// Builds a serverless application as docker image, which includes the base operating system, runtime, and extensions, in addition to your application code and its dependencies
#Build: {
	// Sam configuration
	config: #Config

	// Source code
	fileTree: dagger.#FS

	_run: docker.#Build & {
		steps: [
			#Image,
			docker.#Copy & {
				contents: fileTree
				dest:     _destination
			},
			#Sam & {
				"config": config
				command: args: ["build"]
			},
		]
	}
	output: _run.output
}

// Builds a serverless application as .zip file archive, which contains your application code and its dependencies
#Package: {
	// Sam configuration
	config: #Config

	// Source code
	fileTree: dagger.#FS

	_package: docker.#Build & {
		steps: [
			#Image,
			docker.#Copy & {
				contents: fileTree
				dest:     _destination
			},
			#Sam & {
				"config": config
				command: args: ["package", "--template-file", "template.yaml", "--output-template-file", "output.yaml", "--s3-bucket", config.bucket, "--region", config.region]
			},
		]
	}
	output: _package.output
}

// Verifies whether an AWS SAM template file is valid
#Validate: #Sam & {
	config: _
	command: args: ["validate", "--region", config.region]
}

// Deploying AWS Lambda functions through AWS CloudFormation and save the ZIP in S3 bucket
#DeployZip: #Sam & {
	config: _
	command: args: ["deploy", "--template-file", "output.yaml", "--stack-name", config.stackName, "--capabilities", "CAPABILITY_IAM", "--no-confirm-changeset", "--region", config.region]
}

// Deploying AWS Lambda function and upload docker image to ECR. Matching artifacts are overwritten
#Deployment: #Sam & {
	command: args: ["deploy", "--force-upload"]
}
