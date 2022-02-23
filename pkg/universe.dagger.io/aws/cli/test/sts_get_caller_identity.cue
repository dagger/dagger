package test

import (
	"dagger.io/dagger"
	"universe.dagger.io/aws"
	"universe.dagger.io/aws/cli"
)

dagger.#Plan & {
	inputs: secrets: sops: command: {
		name: "sops"
		args: ["-d", "--extract", "[\"AWS\"]", "../../../secrets_sops.yaml"]
	}

	actions: {
		sopsSecrets: dagger.#DecodeSecret & {
			format: "yaml"
			input:  inputs.secrets.sops.contents
		}

		getCallerIdentity: cli.#Command & {
			credentials: aws.#Credentials & {
				accessKeyId:     sopsSecrets.output.AWS_ACCESS_KEY_ID.contents
				secretAccessKey: sopsSecrets.output.AWS_SECRET_ACCESS_KEY.contents
			}
			options: region: "us-east-2"
			service: {
				name:    "sts"
				command: "get-caller-identity"
			}
		}

		verify: getCallerIdentity.result & {
			UserId:  !~"^$"
			Account: !~"^$"
			Arn:     !~"^$"
		}
	}
}
