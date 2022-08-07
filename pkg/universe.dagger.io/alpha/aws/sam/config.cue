// Use [Sam](https://docs.aws.amazon.com/serverless-application-model/index.html) in a Dagger action
package sam

import (
	"dagger.io/dagger"
)

// Configuration for the SAM project
#Config: {
	// The AWS Region
	region: *null | string

	// The name of the Amazon S3 bucket where this command uploads your AWS CloudFormation template
	bucket: *null | string

	// Prefix added to the artifacts name that are uploaded to the Amazon S3 bucket
	prefix: *null | string

	// Access Key
	accessKey: *null | string

	// Secret Key
	secretKey: dagger.#Secret

	// The name of the AWS CloudFormation stack
	stackName: *null | string

	// The client socket if we wanna build a docker image locally
	clientSocket?: dagger.#Socket

	// The docker tcp host - you need that for building docker images in a ci environment
	host?: string

	// The docker certs path. When you run in DinD-Service it is /certs/client by default
	certs?: dagger.#FS

	// The ciKey indicates if you run in a ci pipeline or locally
	ciKey?: string
}
