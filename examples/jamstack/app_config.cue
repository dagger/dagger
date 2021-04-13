package main

import (
	"dagger.io/git"
)

name: "my-app"

// DISCLAIMER: all values below are fake and are provided as examples

infra: {
	awsConfig: {
		accessKey: "<REPLACE WITH AWS ACCESS KEY>"
		secretKey: "<REPLACE WITH AWS SECRET KEY>"
		region:    "us-east-1"
	}
	vpcId:             "vpc-020ctgv0bcde4242"
	ecrRepository:     "8563296674124.dkr.ecr.us-east-1.amazonaws.com/apps"
	ecsClusterName:    "bl-ecs-acme-764-ECSCluster-lRIVVg09G4HX"
	elbListenerArn:    "arn:aws:elasticloadbalancing:us-east-1:8563296674124:listener/app/bl-ec-ECSAL-OSYI03K07BCO/3c2d3e78347bde5b/d02ac88cc007e24e"
	rdsAdminSecretArn: "arn:aws:secretsmanager:us-east-1:8563296674124:secret:AdminPassword-NQbBi7oU4CYS9-IGgS3B"
	rdsInstanceArn:    "arn:aws:rds:us-east-1:8563296674124:cluster:bl-rds-acme-764-rdscluster-8eg3xbfjggkfdg"
	netlifyAccount: {
		token: "<REPLACE WITH NETLIFY TOKEN>"
	}
}

database: {
	dbType: "mysql"
}

backend: {
	source: git.#Repository & {
		remote: "https://github.com/blocklayerhq/acme-clothing.git"
		ref:    "HEAD"
		subdir: "./crate/code/api"
	}

	// DNS needs to be already configured to the ALB load-balancer
	// and a valid certificate needs to be configured for that listener
	hostname: "\(name).acme-764-api.microstaging.io"

	container: {
		healthCheckPath:    "/health-check"
		healthCheckTimeout: 40
	}
}

frontend: {
	source: git.#Repository & {
		remote: "https://github.com/blocklayerhq/acme-clothing.git"
		ref:    "HEAD"
		subdir: "./crate/code/web"
	}

	writeEnvFile: ".env"

	yarn: {
		buildDir: "public"
		script:   "build:client"
	}
}
