package test

import (
	"encoding/json"
	"dagger.io/dagger"
	"universe.dagger.io/aws"
)

dagger.#Plan & {
	inputs: {
		directories: awsConfig: {
			path: "./"
			include: ["config"]
		}

		secrets: sops: command: {
			name: "sops"
			args: ["-d", "--extract", "[\"AWS\"]", "../../secrets_sops.yaml"]
		}
	}

	actions: {
		sopsSecrets: dagger.#DecodeSecret & {
			format: "yaml"
			input:  inputs.secrets.sops.contents
		}

		getCallerIdentity: aws.#Run & {
			configFile: inputs.directories.awsConfig.contents

			credentials: aws.#Credentials & {
				accessKeyId:     sopsSecrets.output.AWS_ACCESS_KEY_ID.contents
				secretAccessKey: sopsSecrets.output.AWS_SECRET_ACCESS_KEY.contents
			}

			command: {
				name: "sh"
				flags: "-c": true
				args: ["aws --profile ci sts get-caller-identity > /output.txt"]
			}

			export: files: "/output.txt": _
		}

		verify: json.Unmarshal(getCallerIdentity.export.files."/output.txt".contents) & {
			UserId:  string
			Account: =~"^12[0-9]{8}86$"
			Arn:     =~"^arn:aws:sts::(12[0-9]{8}86):assumed-role/dagger-ci"
		}
	}
}
