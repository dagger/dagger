package test

import (
	"dagger.io/dagger"
	"universe.dagger.io/aws"
	"universe.dagger.io/aws/cli"
)

dagger.#Plan & {
	inputs: secrets: {
		AWS_ACCESS_KEY_ID: envvar:     "AWS_ACCESS_KEY_ID"
		AWS_SECRET_ACCESS_KEY: envvar: "AWS_SECRET_ACCESS_KEY"
	}

	actions: getCallerIdentity: cli.#Exec & {
		credentials: aws.#Credentials & {
			accessKeyId:     inputs.secrets.AWS_ACCESS_KEY_ID.contents
			secretAccessKey: inputs.secrets.AWS_SECRET_ACCESS_KEY.contents
		}
		service: "sts"
		cmd: name:       "get-caller-identity"
		options: region: "us-east-2"
		unmarshal: true
		result: {
			UserId:  !~"^$"
			Account: !~"^$"
			Arn:     !~"^$"
		}
	}
}
