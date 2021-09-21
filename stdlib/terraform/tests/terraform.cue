package terraform

import (
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/dagger/op"
	"alpha.dagger.io/alpine"
)

TestData: dagger.#Artifact & dagger.#Input

TestConfig: awsConfig: {
	accessKey: dagger.#Secret & dagger.#Input
	secretKey: dagger.#Secret & dagger.#Input
	region:    "us-east-2"
}

#TestGetConfig: {
	accessKey: dagger.#Secret

	secretKey: dagger.#Secret

	visibleAccessKey: string

	visibleSecretKey: string

	#up: [
		op.#Load & {from: alpine.#Image & {
			package: {
				bash: true
				jq:   true
			}
		}},

		op.#Exec & {
			always: true
			args: ["/bin/bash", "-c", #"""
					export ACCESS_KEY=$(cat /accessKey)
					export SECRET_KEY=$(cat /secretKey)

					jq --arg key0 'visibleAccessKey' --arg value0 "$ACCESS_KEY" \
						 --arg key1 'visibleSecretKey' --arg value1 "$SECRET_KEY" \
						 '. | .[$key0]=$value0 | .[$key1]=$value1' <<< '{}' > /out
				"""#,
			]
			mount: {
				"/accessKey": secret: accessKey
				"/secretKey": secret: secretKey
			}
		},

		op.#Export & {
			source: "/out"
			format: "json"
		},
	]
}

TestTerraform: {
	config: #TestGetConfig & {
		accessKey: TestConfig.awsConfig.accessKey
		secretKey: TestConfig.awsConfig.secretKey
	}

	apply: #Configuration & {
		source: TestData
		env: {
			AWS_ACCESS_KEY_ID:     config.visibleAccessKey
			AWS_SECRET_ACCESS_KEY: config.visibleSecretKey
			AWS_DEFAULT_REGION:    TestConfig.awsConfig.region
			AWS_REGION:            TestConfig.awsConfig.region
		}
	}
}
