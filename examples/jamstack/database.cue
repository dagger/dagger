package main

import (
	"encoding/base64"
	"dagger.io/aws/rds"
)

database: {
	let slug = name
	dbType: "mysql" | "postgresql"

	db: rds.#CreateDB & {
		config:    infra.awsConfig
		name:      slug
		dbArn:     infra.rdsInstanceArn
		"dbType":  dbType
		secretArn: infra.rdsAdminSecretArn
	}

	user: rds.#CreateUser & {
		config:    infra.awsConfig
		dbArn:     infra.rdsInstanceArn
		"dbType":  dbType
		secretArn: infra.rdsAdminSecretArn
		username:  slug
		// FIXME: make it secure (generate infra side?)
		password:      base64.Encode(null, "pwd-\(slug)")
		grantDatabase: db.out
	}

	instance: rds.#Instance & {
		config: infra.awsConfig
		dbArn:  infra.rdsInstanceArn
	}

	hostname: instance.hostname
	port:     instance.port
	dbName:   db.out
	username: user.out
	password: user.password
}
