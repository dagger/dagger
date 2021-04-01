package main

import (
	"dagger.io/aws"
)

// AWS account: credentials and region
awsConfig: aws.#Config & {
	region: *"us-east-1" | string
}

// URL of the website to monitor
website: string | *"https://www.google.com"

// Email address to notify of monitoring alerts
email: string

// The monitoring service running on AWS Cloudwatch
monitor: #HTTPMonitor & {
	notifications: [
		#Notification & {
			endpoint: email
			protocol: "email"
		},
	]
	canaries: [
		#Canary & {
			name: "default"
			url:  website
		},
	]
	cfnStackName: "my-monitor"
	"awsConfig":  awsConfig
}
