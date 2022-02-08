// AWS Relational Database Service (RDS)
package rds

import (
	"alpha.dagger.io/dagger/op"
	"encoding/json"
	"alpha.dagger.io/aws"
)

// Creates a new Database on an existing RDS Instance
#Database: {

	// AWS Config
	config: aws.#Config

	// DB name
	name: string @dagger(input)

	// ARN of the database instance
	dbArn: string @dagger(input)

	// ARN of the database secret (for connecting via rds api)
	secretArn: string @dagger(input)

	// Database type MySQL or PostgreSQL (Aurora Serverless only)
	dbType: "mysql" | "postgres" @dagger(input)

	// Name of the DB created
	out: {
		@dagger(output)
		string

		#up: [
			op.#Load & {
				from: aws.#CLI & {
					"config": config
				}
			},

			op.#Exec & {
				args: [
					"/bin/bash",
					"--noprofile",
					"--norc",
					"-eo",
					"pipefail",
					#"""
						echo "dbType: $DB_TYPE"

						sql="CREATE DATABASE \`"$NAME" \`"
						if [ "$DB_TYPE" = postgres ]; then
							sql="CREATE DATABASE \""$NAME"\""
						fi

						echo "$NAME" >> /db_created

						aws rds-data execute-statement \
							--resource-arn "$DB_ARN" \
							--secret-arn "$SECRET_ARN" \
							--sql "$sql" \
							--database "$DB_TYPE" \
							--no-include-result-metadata \
						|& tee /tmp/out
						exit_code=${PIPESTATUS[0]}
						if [ $exit_code -ne 0 ]; then
							grep -q "database exists\|already exists" /tmp/out || exit $exit_code
						fi
						"""#,
				]
				env: {
					NAME:       name
					DB_ARN:     dbArn
					SECRET_ARN: secretArn
					DB_TYPE:    dbType
				}
			},

			op.#Export & {
				source: "/db_created"
				format: "string"
			},
		]
	}
}

// Creates a new user credentials on an existing RDS Instance
#User: {

	// AWS Config
	config: aws.#Config

	// Username
	username: string @dagger(input)

	// Password
	password: string @dagger(input)

	// ARN of the database instance
	dbArn: string @dagger(input)

	// ARN of the database secret (for connecting via rds api)
	secretArn: string @dagger(input)

	// Name of the database to grants access to
	grantDatabase: string | *"" @dagger(input)

	// Database type MySQL or PostgreSQL (Aurora Serverless only)
	dbType: "mysql" | "postgres" @dagger(input)

	// Outputted username
	out: {
		string

		#up: [
			op.#Load & {
				from: aws.#CLI & {
					"config": config
				}
			},

			op.#Exec & {
				args: [
					"/bin/bash",
					"--noprofile",
					"--norc",
					"-eo",
					"pipefail",
					#"""
						echo "dbType: $DB_TYPE"

						sql="CREATE USER '"$USERNAME"'@'%' IDENTIFIED BY '"$PASSWORD"'"
						if [ "$DB_TYPE" = postgres ]; then
							sql="CREATE USER \""$USERNAME"\" WITH PASSWORD '"$PASSWORD"'"
						fi

						echo "$USERNAME" >> /username

						aws rds-data execute-statement \
							--resource-arn "$DB_ARN" \
							--secret-arn "$SECRET_ARN" \
							--sql "$sql" \
							--database "$DB_TYPE" \
							--no-include-result-metadata \
						|& tee tmp/out
						exit_code=${PIPESTATUS[0]}
						if [ $exit_code -ne 0 ]; then
							grep -q "Operation CREATE USER failed for\|ERROR" tmp/out || exit $exit_code
						fi

						sql="SET PASSWORD FOR '"$USERNAME"'@'%' = PASSWORD('"$PASSWORD"')"
						if [ "$DB_TYPE" = postgres ]; then
							sql="ALTER ROLE \""$USERNAME"\" WITH PASSWORD '"$PASSWORD"'"
						fi

						aws rds-data execute-statement \
							--resource-arn "$DB_ARN" \
							--secret-arn "$SECRET_ARN" \
							--sql "$sql" \
							--database "$DB_TYPE" \
							--no-include-result-metadata

						sql="GRANT ALL ON \`"$GRAND_DATABASE"\`.* to '"$USERNAME"'@'%'"
						if [ "$DB_TYPE" = postgres ]; then
							sql="GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA public TO \""$USERNAME"\"; GRANT ALL PRIVILEGES ON DATABASE \""$GRAND_DATABASE"\" to \""$USERNAME"\"; GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public TO \""$USERNAME"\"; ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT ALL PRIVILEGES ON TABLES TO \""$USERNAME"\"; ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT ALL PRIVILEGES ON SEQUENCES TO \""$USERNAME"\"; GRANT USAGE ON SCHEMA public TO \""$USERNAME"\";"
						fi

						if [ -s "$GRAND_DATABASE ]; then
							aws rds-data execute-statement \
								--resource-arn "$DB_ARN" \
								--secret-arn "$SECRET_ARN" \
								--sql "$sql" \
								--database "$DB_TYPE" \
								--no-include-result-metadata
						fi
						"""#,
				]
				env: {
					USERNAME:       username
					PASSWORD:       password
					DB_ARN:         dbArn
					SECRET_ARN:     secretArn
					GRAND_DATABASE: grantDatabase
					DB_TYPE:        dbType
				}
			},

			op.#Export & {
				source: "/username"
				format: "string"
			},
		]
	} @dagger(output)
}

// Fetches information on an existing RDS Instance
#Instance: {

	// AWS Config
	config: aws.#Config

	// ARN of the database instance
	dbArn: string @dagger(input)

	// DB hostname
	hostname: info.hostname @dagger(output)

	// DB port
	port: info.port @dagger(output)

	info: {
		hostname: string
		port:     int
	}

	info: json.Unmarshal(out) @dagger(output)
	out: {
		string

		#up: [
			op.#Load & {
				from: aws.#CLI & {
					"config": config
				}
			},

			op.#Exec & {
				args: [
					"/bin/bash",
					"--noprofile",
					"--norc",
					"-eo",
					"pipefail",
					#"""
						data=$(aws rds describe-db-clusters --filters "Name=db-cluster-id,Values=$DB_URN" )
						echo "$data" | jq -r '.DBClusters[].Endpoint' > /tmp/out
						echo "$data" | jq -r '.DBClusters[].Port' >> /tmp/out
						cat /tmp/out | jq -sR 'split("\n") | {hostname: .[0], port: (.[1] | tonumber)}' > /out
						"""#,
				]
				env: DB_ARN: dbArn
			},

			op.#Export & {
				source: "/out"
				format: "json"
			},
		]
	}
}
