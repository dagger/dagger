// AWS base package
package aws

import (
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/dagger/op"
	"alpha.dagger.io/alpine"
)

// AWS Config shared by all AWS packages
#Config: {
	// AWS region
	region: dagger.#Input & { string }
	// AWS access key
	accessKey: dagger.#Input & { dagger.#Secret }
	// AWS secret key
	secretKey: dagger.#Input & { dagger.#Secret }
	// AWS localstack mode
	localMode: dagger.#Input & { string | *null }
}

// Re-usable aws-cli component
#CLI: {
	config: #Config
	package: [string]: string | bool

	#up: [
		op.#Load & {
			from: alpine.#Image & {
				"package": package
				"package": bash:      "=~5.1"
				"package": jq:        "=~1.6"
				"package": curl:      true
				"package": "aws-cli": "=~1.18"
				if config.localMode != null {
					"package": "py3-pip": true
				}
			}
		},
		op.#Exec & {
			args: [
				"/bin/bash",
				"--noprofile",
				"--norc",
				"-eo",
				"pipefail",
				"-c",
				if config.localMode == null {
                    #"""
                    aws configure set aws_access_key_id "$(cat /run/secrets/access_key)"
                    aws configure set aws_secret_access_key "$(cat /run/secrets/secret_key)"

                    aws configure set default.region "$AWS_DEFAULT_REGION"
                    aws configure set default.cli_pager ""
                    aws configure set default.output "json"
                    """#,
                }
                if config.localMode != null {
                    #"""
                    # Download awscli v3 and override aws
                    pip install awscli-local[v2]
                    mv /usr/bin/awslocal /usr/bin/aws

                    # Configure
                    mkdir -p ~/.aws/

                    # Set up ~/.aws/config
                    echo "[default]" > ~/.aws/config
                    echo "region = $AWS_DEFAULT_REGION" >> ~/.aws/config
                    echo "cli_pager =" >> ~/.aws/config
                    echo "output = json" >> ~/.aws/config

                    # Set up ~/.aws/credentials
                    echo "[default]" > ~/.aws/credentials
                    echo "aws_access_key_id = $(cat /run/secrets/access_key)" >> ~/.aws/credentials
                    echo "aws_secret_access_key = $(cat /run/secrets/secret_key)" >> ~/.aws/credentials
                    """#,
				}
			]
			mount: "/run/secrets/access_key": secret: config.accessKey
			mount: "/run/secrets/secret_key": secret: config.secretKey
			env: AWS_DEFAULT_REGION: config.region
		},
	]
}
