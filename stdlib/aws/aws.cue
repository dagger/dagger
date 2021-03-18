package aws

import (
	"dagger.io/dagger"
	"dagger.io/llb"
	"dagger.io/alpine"
)

// Base AWS Config
#Config: {
	// AWS region
	region: string
	// AWS access key
	accessKey: dagger.#Secret
	// AWS secret key
	secretKey: dagger.#Secret
}

// Re-usable aws-cli component
#CLI: {
	#compute: [
		llb.#Load & {
			from: alpine.#Image & {
				package: bash:      "=5.1.0-r0"
				package: jq:        "=1.6-r1"
				package: curl:      "=7.74.0-r1"
				package: "aws-cli": "=1.18.177-r0"
			}
		},
	]
}
