package test

import (
	"dagger.io/dagger"
	"universe.dagger.io/aws"
)

dagger.#Plan & {
	inputs: secrets: {
		AWS_ACCESS_KEY_ID: envvar:     "AWS_ACCESS_KEY_ID"
		AWS_SECRET_ACCESS_KEY: envvar: "AWS_SECRET_ACCESS_KEY"
	}

	actions: {
		config: aws.#Config & {
			AWS_REGION:            "us-east-2"
			AWS_ACCESS_KEY_ID:     inputs.secrets.AWS_ACCESS_KEY_ID.contents
			AWS_SECRET_ACCESS_KEY: inputs.secrets.AWS_SECRET_ACCESS_KEY.contents
		}

		getCallerIdentity: aws.#CLI & {
			"config": config
			service:  "sts"
			cmd: name: "get-caller-identity"
			unmarshal: true
			result: {
				UserId:  !~"^$"
				Account: !~"^$"
				Arn:     !~"^$"
			}
		}

	}
}
