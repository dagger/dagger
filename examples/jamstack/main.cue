package main

// Name of the application
name: string & =~"[a-z0-9-]+"

// FIXME: temporary workaround (GH issue #142) - image metadata is lost after build
backend: container: command: ["/bin/hello-go"]

// Inject db info in the container environment
backend: environment: {
	DB_USERNAME: database.username
	DB_HOSTNAME: database.hostname
	DB_PASSWORD: database.password
	DB_DBNAME:   database.dbName
	DB_PORT:     "\(database.port)"
	DB_TYPE:     database.dbType
}

url: {
	frontendURL: "FIXME"
	backendURL:  "https://\(backend.hostname)/"
}
