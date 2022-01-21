// AWS base package
package aws

import (
	"strings"
	"encoding/json"
	"dagger.io/dagger"
	"dagger.io/dagger/engine"
	"universe.dagger.io/alpine"
	"universe.dagger.io/docker"
)

_#regions: "us-east-2" | "us-east-1" | "us-west-1" | "us-west-2" | "af-south-1" | "ap-east-1" | "ap-southeast-3" | "ap-south-1" | "ap-northeast-3" | "ap-northeast-2" | "ap-southeast-1" | "ap-southeast-2" | "ap-northeast-1" | "ca-central-1" | "cn-north-1" | "cn-northwest-1" | "eu-central-1" | "eu-west-1" | "eu-west-2" | "eu-south-1" | "eu-west-3" | "eu-north-1" | "me-south-1" | "sa-east-1"

_#output: *"json" | "text" | "table" | "yaml" | "yaml-stream"

// AWS Config written to .aws/config and .aws/credentials
#Config: {
	// AWS access key
	AWS_ACCESS_KEY_ID?: dagger.#Secret

	// AWS secret key
	AWS_SECRET_ACCESS_KEY?: dagger.#Secret

	// AWS session token
	AWS_SESSION_TOKEN?: dagger.#Secret

	// AWS region
	AWS_REGION: _#regions

	// Output format
	AWS_DEFAULT_OUTPUT: _#output
}

#CLI: {
	// Global config options
	config: #Config

	// The version of the AWS CLI to install
	version: string | *"2.4.12"

	// Additional packages to install to the Alpine image
	packages: alpine.#Build.packages

	// Global arguments passed to the aws cli.
	options: {
		// Turn on debug logging.
		debug?: bool

		// Override command's default URL with the given URL.
		"endpoint-url"?: string

		// By  default, the AWS CLI uses SSL when communicating with AWS services.
		// For each SSL connection, the AWS CLI will verify SSL certificates. This
		// option overrides the default behavior of verifying SSL certificates.
		"no-verify-ssl"?: bool

		// Disable automatic pagination.
		"no-paginate"?: bool

		// The formatting style for command output.
		output: _#output

		// A JMESPath query to use in filtering the response data.
		query?: string

		// Use a specific profile from your credential file.
		profile?: string

		// The region to use. Overrides config/env settings.
		region?: string

		// Display the version of this tool.
		version?: bool

		// Turn on/off color output.
		color?: "off" | "on" | "auto"

		// Do  not  sign requests. Credentials will not be loaded if this argument
		// is provided.
		"no-sign-request"?: bool

		// The CA certificate bundle to use when verifying SSL certificates. Over-
		// rides config/env settings.
		"ca-bundle"?: string

		// The  maximum socket read time in seconds. If the value is set to 0, the
		// socket read will be blocking and not timeout. The default value  is  60
		// seconds.
		"cli-read-timeout"?: int

		// The  maximum  socket connect time in seconds. If the value is set to 0,
		// the socket connect will be blocking and not timeout. The default  value
		// is 60 seconds.
		"cli-connect-timeout"?: int

		// The formatting style to be used for binary blobs. The default format is
		// base64. The base64 format expects binary blobs  to  be  provided  as  a
		// base64  encoded string. The raw-in-base64-out format preserves compati-
		// bility with AWS CLI V1 behavior and binary values must be passed liter-
		// ally.  When  providing  contents  from a file that map to a binary blob
		// fileb:// will always be treated as binary and  use  the  file  contents
		// directly  regardless  of  the  cli-binary-format  setting.  When  using
		// file:// the file contents will need to properly formatted for the  con-
		// figured cli-binary-format.
		"cli-binary-format"?: "base64" | "raw-in-base64-out"

		// Disable cli pager for output.
		"no-cli-pager": true

		// Automatically prompt for CLI input parameters.
		"cli-auto-prompt"?: bool

		// Disable automatically prompt for CLI input parameters.
		"no-cli-auto-prompt"?: bool
	}

	// The aws service command to run.
	// example: describe-instances
	cmd: {
		name: string
		args: [...string]
	}

	// The service to run the command against.
	service: "accessanalyzer" | "account" | "acm" | "acm-pca" | "alexaforbusiness" | "amp" | "amplify" | "amplifybackend" | "amplifyuibuilder" | "apigateway" | "apigatewaymanagementapi" | "apigatewayv2" | "appconfig" | "appconfigdata" | "appflow" | "appintegrations" | "application-autoscaling" | "application-insights" | "applicationcostprofiler" | "appmesh" | "apprunner" | "appstream" | "appsync" | "athena" | "auditmanager" | "autoscaling" | "autoscaling-plans" | "backup" | "backup-gateway" | "batch" | "braket" | "budgets" | "ce" | "chime" | "chime-sdk-identity" | "chime-sdk-meetings" | "chime-sdk-messaging" | "cli-dev" | "cloud9" | "cloudcontrol" | "clouddirectory" | "cloudformation" | "cloudfront" | "cloudhsm" | "cloudtrail" | "cloudwatch" | "codeartifact" | "codebuild" | "codecommit" | "codeguru-reviewer" | "codeguruprofiler" | "codepipeline" | "codestar" | "codestar-connections" | "codestar-notifications" | "cognito-identity" | "cognito-idp" | "cognito-sync" | "comprehend" | "comprehendmedical" | "compute-optimizer" | "configservice" | "configure" | "connect" | "connect-contact-lens" | "connectparticipant" | "cur" | "customer-profiles" | "databrew" | "dataexchange" | "datapipeline" | "datasync" | "dax" | "ddb" | "deploy" | "detective" | "devicefarm" | "devops-guru" | "directconnect" | "discovery" | "dlm" | "dms" | "docdb" | "drs" | "ds" | "dynamodb" | "dynamodbstreams" | "ebs" | "ec2" | "ec2-instance-connect" | "ecr" | "ecr-public" | "ecs" | "efs" | "eks" | "elastic-inference" | "elasticache" | "elasticbeanstalk" | "elastictranscoder" | "elb" | "elbv2" | "emr" | "emr-containers" | "es" | "events" | "evidently" | "finspace" | "finspace-data" | "firehose" | "fis" | "fms" | "forecast" | "forecastquery" | "frauddetector" | "fsx" | "gamelift" | "glacier" | "globalaccelerator" | "glue" | "grafana" | "greengrass" | "greengrassv2" | "groundstation" | "guardduty" | "health" | "healthlake" | "help" | "history" | "honeycode" | "iam" | "identitystore" | "imagebuilder" | "importexport" | "inspector" | "inspector2" | "iot" | "iot-data" | "iot-jobs-data" | "iot1click-devices" | "iot1click-projects" | "iotanalytics" | "iotdeviceadvisor" | "iotevents" | "iotevents-data" | "iotfleethub" | "iotsecuretunneling" | "iotsitewise" | "iotthingsgraph" | "iottwinmaker" | "iotwireless" | "ivs" | "kafka" | "kafkaconnect" | "kendra" | "kinesis" | "kinesis-video-archived-media" | "kinesis-video-media" | "kinesis-video-signaling" | "kinesisanalytics" | "kinesisanalyticsv2" | "kinesisvideo" | "kms" | "lakeformation" | "lambda" | "lex-models" | "lex-runtime" | "lexv2-models" | "lexv2-runtime" | "license-manager" | "lightsail" | "location" | "logs" | "lookoutequipment" | "lookoutmetrics" | "lookoutvision" | "machinelearning" | "macie" | "macie2" | "managedblockchain" | "marketplace-catalog" | "marketplace-entitlement" | "marketplacecommerceanalytics" | "mediaconnect" | "mediaconvert" | "medialive" | "mediapackage" | "mediapackage-vod" | "mediastore" | "mediastore-data" | "mediatailor" | "memorydb" | "meteringmarketplace" | "mgh" | "mgn" | "migration-hub-refactor-spaces" | "migrationhub-config" | "migrationhubstrategy" | "mobile" | "mq" | "mturk" | "mwaa" | "neptune" | "network-firewall" | "networkmanager" | "nimble" | "opensearch" | "opsworks" | "opsworks-cm" | "organizations" | "outposts" | "panorama" | "personalize" | "personalize-events" | "personalize-runtime" | "pi" | "pinpoint" | "pinpoint-email" | "pinpoint-sms-voice" | "polly" | "pricing" | "proton" | "qldb" | "qldb-session" | "quicksight" | "ram" | "rbin" | "rds" | "rds-data" | "redshift" | "redshift-data" | "rekognition" | "resiliencehub" | "resource-groups" | "resourcegroupstaggingapi" | "robomaker" | "route53" | "route53-recovery-cluster" | "route53-recovery-control-config" | "route53-recovery-readiness" | "route53domains" | "route53resolver" | "rum" | "s3" | "s3api" | "s3control" | "s3outposts" | "sagemaker" | "sagemaker-a2i-runtime" | "sagemaker-edge" | "sagemaker-featurestore-runtime" | "sagemaker-runtime" | "savingsplans" | "schemas" | "sdb" | "secretsmanager" | "securityhub" | "serverlessrepo" | "service-quotas" | "servicecatalog" | "servicecatalog-appregistry" | "servicediscovery" | "ses" | "sesv2" | "shield" | "signer" | "sms" | "snow-device-management" | "snowball" | "sns" | "sqs" | "ssm" | "ssm-contacts" | "ssm-incidents" | "sso" | "sso-admin" | "sso-oidc" | "stepfunctions" | "storagegateway" | "sts" | "support" | "swf" | "synthetics" | "textract" | "timestream-query" | "timestream-write" | "transcribe" | "transfer" | "translate" | "voice-id" | "waf" | "waf-regional" | "wafv2" | "wellarchitected" | "wisdom" | "workdocs" | "worklink" | "workmail" | "workmailmessageflow" | "workspaces" | "workspaces-web" | "xray" | ""

	unmarshal: false | *true

	image: docker.#Image | *_build.output

	// Filesystem mounts
	mounts: [name=string]: engine.#Mount

	_build: alpine.#Build & {
		"packages": packages
		"packages": {
			curl: {}
			// "py3-pip": {}
		}
	}

	_install: docker.#Run & {
		"image": image
		script:  """
			curl -sL https://alpine-pkgs.sgerrand.com/sgerrand.rsa.pub -o /etc/apk/keys/sgerrand.rsa.pub 
			curl -sLO https://github.com/sgerrand/alpine-pkg-glibc/releases/download/2.31-r0/glibc-2.31-r0.apk 
			curl -sLO https://github.com/sgerrand/alpine-pkg-glibc/releases/download/2.31-r0/glibc-bin-2.31-r0.apk 
			curl -sLO https://github.com/sgerrand/alpine-pkg-glibc/releases/download/2.31-r0/glibc-i18n-2.31-r0.apk 
			apk add --no-cache glibc-2.31-r0.apk glibc-bin-2.31-r0.apk glibc-i18n-2.31-r0.apk 
			/usr/glibc-compat/bin/localedef -i en_US -f UTF-8 en_US.UTF-8 
			ARCH=$(uname -m)
			curl -s https://awscli.amazonaws.com/awscli-exe-linux-${ARCH}-\(version).zip -o awscliv2.zip
			unzip awscliv2.zip
			./aws/install
			rm -rf awscliv2.zip aws /usr/local/aws-cli/v2/*/dist/aws_completer /usr/local/aws-cli/v2/*/dist/awscli/data/ac.index /usr/local/aws-cli/v2/*/dist/awscli/examples glibc-*.apk
			"""
	}

	_shim: {
		_scriptPreface: [string]: string
		for k, v in config {
			// pass all *non secrets* as env vars
			if (v & string) != _|_ {
				env: "\(k)": v
			}

			// pass all secrets as secret mounts
			if (v & string) == _|_ {
				_scriptPreface: "\(k)": "export \(k)=$(cat /run/secrets/\(k))"
				mounts: "\(k)": {
					dest:     "/run/secrets/\(k)"
					contents: v
				}
			}
		}
	}

	_options: [ for optName, opt in options {
		if (opt & true) != _|_ {
			"--\(optName)"
		}
		if (opt & string) != _|_ {
			"--\(optName) \(opt)"
		}
	}]

	_scriptPrefaceList: [ for _, s in _shim._scriptPreface {s}] + ["aws \(strings.Join(_options, " ")) \(service) \(cmd.name) \(strings.Join(cmd.args, " ")) > /output.txt"]

	_run: docker.#Run & {
		image: _install.output
		_shim
		"mounts": mounts
		script:   strings.Join(_scriptPrefaceList, "\n")
		export: files: "/output.txt": _
	}

	output: _run.output
	export: _run.export

	_unmarshalable: string | number | bool | null | [..._unmarshalable] | {[string]: _unmarshalable}
	result?:        _unmarshalable

	if unmarshal != false {
		options: output: "json"
		result: json.Unmarshal(export.files["/output.txt"].contents)
	}
}
