// AWS base package
package aws

import (
	"dagger.io/dagger"
)

#Regions: "us-east-2" | "us-east-1" | "us-west-1" | "us-west-2" | "af-south-1" | "ap-east-1" | "ap-southeast-3" | "ap-south-1" | "ap-northeast-3" | "ap-northeast-2" | "ap-southeast-1" | "ap-southeast-2" | "ap-northeast-1" | "ca-central-1" | "cn-north-1" | "cn-northwest-1" | "eu-central-1" | "eu-west-1" | "eu-west-2" | "eu-south-1" | "eu-west-3" | "eu-north-1" | "me-south-1" | "sa-east-1"

// Credentials provides long or short-term credentials.
#Credentials: {
	// AWS access key
	accessKeyId?: dagger.#Secret

	// AWS secret key
	secretAccessKey?: dagger.#Secret

	// AWS session token (provided with temporary credentials)
	sessionToken?: dagger.#Secret
}
