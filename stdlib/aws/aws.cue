// AWS base package
package aws

import (
	"regexp"
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/dagger/op"
	"alpha.dagger.io/alpine"
)

// AWS Config shared by all AWS packages
#Config: {
	// AWS region
	region: dagger.#Input & {string}
	// AWS access key
	accessKey: dagger.#Input & {dagger.#Secret}
	// AWS secret key
	secretKey: dagger.#Input & {dagger.#Secret}
	// AWS localstack mode
	localMode: dagger.#Input & {*false | bool}
}

// Configuration specific to CLI v1
#V1: {
	config: #Config
	package: [string]: string | bool
	version: dagger.#Input & {*"1.18" | string}

	#up: [
		op.#Load & {
			from: alpine.#Image & {
				"package": package
				"package": bash:      "=~5.1"
				"package": jq:        "=~1.6"
				"package": curl:      true
				"package": "aws-cli": "=~\( version )"
				if config.localMode != false {
					package: "py3-pip": true
				}
			}

		},
	]
}

// Configuration specific to CLI v2
#V2: {
	config: #Config
	package: [string]: string | bool
	version: dagger.#Input & {*"2.1.27" | string}

	#up: [
		op.#Load & {
			from: alpine.#Image & {
				"package": package
				"package": bash:     "=~5.1"
				"package": jq:       "=~1.6"
				"package": curl:     true
				"package": binutils: true
				if config.localMode != false {
					package: "py3-pip": true
				}
			}
		},
		//https://stackoverflow.com/a/61268529
		op.#Exec & {
			env: AWS_CLI_VERSION: version
			args: ["/bin/bash", "--noprofile", "--norc", "-eo", "pipefail", "-c",
				#"""
						curl -sL https://alpine-pkgs.sgerrand.com/sgerrand.rsa.pub -o /etc/apk/keys/sgerrand.rsa.pub 
						curl -sLO https://github.com/sgerrand/alpine-pkg-glibc/releases/download/2.31-r0/glibc-2.31-r0.apk 
						curl -sLO https://github.com/sgerrand/alpine-pkg-glibc/releases/download/2.31-r0/glibc-bin-2.31-r0.apk 
						curl -sLO https://github.com/sgerrand/alpine-pkg-glibc/releases/download/2.31-r0/glibc-i18n-2.31-r0.apk 
						apk add --no-cache glibc-2.31-r0.apk glibc-bin-2.31-r0.apk glibc-i18n-2.31-r0.apk 
						/usr/glibc-compat/bin/localedef -i en_US -f UTF-8 en_US.UTF-8 

						curl -s https://awscli.amazonaws.com/awscli-exe-linux-x86_64-${AWS_CLI_VERSION}.zip -o awscliv2.zip
						unzip awscliv2.zip > /dev/null
						./aws/install
						rm -rf awscliv2.zip aws /usr/local/aws-cli/v2/*/dist/aws_completer /usr/local/aws-cli/v2/*/dist/awscli/data/ac.index \
						usr/local/aws-cli/v2/*/dist/awscli/examples glibc-*.apk
					"""#]
		},
	]
}

#CLI: {
	config: #Config
	package: [string]: string | bool
	version: dagger.#Input & {*"1.18" | string}

	_isV2: regexp.Match("^2.*$", version)

	#up: [
		op.#Load & {
			if _isV2 == false {
				from: #V1 & {
					"config":  config
					"package": package
					"version": version
				}
			}
			if _isV2 == true {
				from: #V2 & {
					"config":  config
					"package": package
					"version": version
				}
			}

		},
		op.#Exec & {
			if config.localMode == false {
				args: ["/bin/bash", "--noprofile", "--norc", "-eo", "pipefail", "-c",
					#"""
							aws configure set aws_access_key_id "$(cat /run/secrets/access_key)"
							aws configure set aws_secret_access_key "$(cat /run/secrets/secret_key)"

							aws configure set default.region "$AWS_DEFAULT_REGION"
							aws configure set default.cli_pager ""
							aws configure set default.output "json"
						"""#]
			}
			if config.localMode == true {
				args: [ "/bin/bash", "--noprofile", "--norc", "-eo", "pipefail", "-c",
					#"""
							# Download awscli v3 and override aws
							pip install awscli-local==0.14
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
						"""#]
			}
			mount: "/run/secrets/access_key": secret: config.accessKey
			mount: "/run/secrets/secret_key": secret: config.secretKey
			env: AWS_DEFAULT_REGION: config.region
		},
	]
}
