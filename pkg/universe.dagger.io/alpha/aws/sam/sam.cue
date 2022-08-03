package sam

import (
	"dagger.io/dagger"
	"universe.dagger.io/docker"
)

// Working directory in Sam image
_destination: "/var/task"

// AWS Sam image
#DefaultImage: docker.#Pull & {
	source: "amazon/aws-sam-cli-build-image-provided:latest"
}

// Executes the sam command
#Sam: {
	// Default AWS Sam docker image
	_defaultImage: #DefaultImage
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
	config:   #Config

	// Source code
	fileTree: dagger.#FS

	_run: docker.#Build & {
		steps: [
			#DefaultImage,
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
	config:   #Config

	// Source code
	fileTree: dagger.#FS

	_package: docker.#Build & {
		steps: [
			#DefaultImage,
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