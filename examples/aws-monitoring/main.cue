package main

import (
	"dagger.io/aws"
)

// Fill using:
//          --input-string awsConfig.accessKey=XXX
//          --input-string awsConfig.secretKey=XXX
awsConfig: aws.#Config & {
	region: *"us-east-1" | string
}

monitor: #HTTPMonitor & {
	notifications: [
		#Notification & {
			endpoint: "sam+test@blocklayerhq.com"
			protocol: "email"
		},
	]
	canaries: [
		#Canary & {
			name: "website-test"
			url:  "https://www.google.com/"
		},
	]
	cfnStackName: "my-monitor"
	"awsConfig":  awsConfig
}
