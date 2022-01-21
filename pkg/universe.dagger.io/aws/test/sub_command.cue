package test

import (
	"encoding/json"
	"dagger.io/dagger"
	"dagger.io/dagger/engine"
	"universe.dagger.io/aws"
)

dagger.#Plan & {
	inputs: secrets: aws: command: {
		name: "aws-vault"
		args: ["exec", "tmj", "--json", "--no-session"]
	}

	actions: {
		creds: engine.#TransformSecret & {
			input: inputs.secrets.aws.contents
			#function: {
				input:  _
				output: json.Unmarshal(input)
			}
		}

		config: aws.#Config & {
			region:    "us-east-2"
			accessKey: creds.output.AccessKeyId.contents
			secretKey: creds.output.SecretAccessKey.contents
			// sessionToken: creds.output.SessionToken.contents
			// expiration:   creds.output.Expiration.contents
		}

		"get-caller-identity": aws.#CLI & {
			"config": config
			service:  "sts"
			cmd: name: "get-caller-identity"
			export: files: "/output.txt": contents: =~"richard"
		}
	}
}
