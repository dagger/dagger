package rds

import (
	"encoding/json"
	"dagger.io/dagger"
	"dagger.io/aws"
)

#CreateDB: {
	// AWS Config
	config: aws.#Config

	// DB name
	name: string

	// ARN of the database instance
	dbArn: string

	// ARN of the database secret (for connecting via rds api)
	secretArn: string

	dbType: "mysql" | "postgres"

	// Name of the DB created
	out: string

	aws.#Script & {
		"config": config

		files: {
			"/inputs/name":       name
			"/inputs/db_arn":     dbArn
			"/inputs/secret_arn": secretArn
			"/inputs/db_type":    dbType
		}

		export: "/db_created"

		code: #"""
			set +o pipefail

			dbType="$(cat /inputs/db_type)"
			echo "dbType: $dbType"

			sql="CREATE DATABASE \`$(cat /inputs/name)\`"
			if [ "$dbType" = postgres ]; then
			    sql="CREATE DATABASE \"$(cat /inputs/name)\""
			fi

			cp /inputs/name /db_created

			aws rds-data execute-statement \
			    --resource-arn "$(cat /inputs/db_arn)" \
			    --secret-arn "$(cat /inputs/secret_arn)" \
			    --sql "$sql" \
			    --database "$dbType" \
			    --no-include-result-metadata \
			|& tee /tmp/out
			exit_code=${PIPESTATUS[0]}
			if [ $exit_code -ne 0 ]; then
			    grep -q "database exists\|already exists" /tmp/out || exit $exit_code
			fi
			"""#
	}
}

#CreateUser: {
	// AWS Config
	config: aws.#Config

	// Username
	username: dagger.#Secret

	// Password
	password: dagger.#Secret

	// ARN of the database instance
	dbArn: string

	// ARN of the database secret (for connecting via rds api)
	secretArn: string

	grantDatabase: string | *""

	dbType: "mysql" | "postgres"

	// Outputed username
	out: string

	aws.#Script & {
		"config": config

		files: {
			"/inputs/username":       username
			"/inputs/password":       password
			"/inputs/db_arn":         dbArn
			"/inputs/secret_arn":     secretArn
			"/inputs/grant_database": grantDatabase
			"/inputs/db_type":        dbType
		}

		export: "/username"

		code: #"""
			set +o pipefail

			dbType="$(cat /inputs/db_type)"
			echo "dbType: $dbType"

			sql="CREATE USER '$(cat /inputs/username)'@'%' IDENTIFIED BY '$(cat /inputs/password)'"
			if [ "$dbType" = postgres ]; then
			    sql="CREATE USER \"$(cat /inputs/username)\" WITH PASSWORD '$(cat /inputs/password)'"
			fi

			cp /inputs/username /username

			aws rds-data execute-statement \
			    --resource-arn "$(cat /inputs/db_arn)" \
			    --secret-arn "$(cat /inputs/secret_arn)" \
			    --sql "$sql" \
			    --database "$dbType" \
			    --no-include-result-metadata \
			|& tee tmp/out
			exit_code=${PIPESTATUS[0]}
			if [ $exit_code -ne 0 ]; then
			    grep -q "Operation CREATE USER failed for\|ERROR" tmp/out || exit $exit_code
			fi

			sql="SET PASSWORD FOR '$(cat /inputs/username)'@'%' = PASSWORD('$(cat /inputs/password)')"
			if [ "$dbType" = postgres ]; then
			    sql="ALTER ROLE \"$(cat /inputs/username)\" WITH PASSWORD '$(cat /inputs/password)'"
			fi

			aws rds-data execute-statement \
			    --resource-arn "$(cat /inputs/db_arn)" \
			    --secret-arn "$(cat /inputs/secret_arn)" \
			    --sql "$sql" \
			    --database "$dbType" \
			    --no-include-result-metadata

			sql="GRANT ALL ON \`$(cat /inputs/grant_database)\`.* to '$(cat /inputs/username)'@'%'"
			if [ "$dbType" = postgres ]; then
			    sql="GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA public TO \"$(cat /inputs/username)\"; GRANT ALL PRIVILEGES ON DATABASE \"$(cat /inputs/grant_database)\" to \"$(cat /inputs/username)\"; GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public TO \"$(cat /inputs/username)\"; ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT ALL PRIVILEGES ON TABLES TO \"$(cat /inputs/username)\"; ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT ALL PRIVILEGES ON SEQUENCES TO \"$(cat /inputs/username)\"; GRANT USAGE ON SCHEMA public TO \"$(cat /inputs/username)\";"
			fi

			if [ -s /inputs/grant_database ]; then
			    aws rds-data execute-statement \
			        --resource-arn "$(cat /inputs/db_arn)" \
			        --secret-arn "$(cat /inputs/secret_arn)" \
			        --sql "$sql" \
			        --database "$dbType" \
			        --no-include-result-metadata
			fi
			"""#
	}
}

#Instance: {
	// AWS Config
	config: aws.#Config

	// ARN of the database instance
	dbArn: string

	// DB hostname
	hostname: info.hostname

	// DB port
	port: info.port

	info: {
		hostname: string
		port:     int
	}

	info: json.Unmarshal(out)
	out:  string

	aws.#Script & {
		"config": config

		files: "/inputs/db_arn": dbArn

		export: "/out"

		code: #"""
			db_arn="$(cat /inputs/db_arn)"
			data=$(aws rds describe-db-clusters --filters "Name=db-cluster-id,Values=$db_arn" )
			echo "$data" | jq -r '.DBClusters[].Endpoint' > /tmp/out
			echo "$data" | jq -r '.DBClusters[].Port' >> /tmp/out
			cat /tmp/out | jq -sR 'split("\n") | {hostname: .[0], port: (.[1] | tonumber)}' > /out
			"""#
	}
}
