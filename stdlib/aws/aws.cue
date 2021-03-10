package aws

import "dagger.io/dagger"

#Config: {
	// AWS region
	region: string
	// AWS access key
	accessKey: dagger.#Secret
	// AWS secret key
	secretKey: dagger.#Secret
}
