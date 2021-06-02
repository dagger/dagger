package main

// Name of the application
name: string & =~"[a-z0-9-]+" @dagger(input)

// Inject db info in the container environment
backend: environment: {
	DB_USERNAME: database.username
	DB_HOSTNAME: database.hostname
	DB_PASSWORD: database.password
	DB_DBNAME:   database.dbName
	DB_PORT:     "\(database.port)"
	DB_TYPE:     database.dbType
}

// Configure the frontend with the API URL
frontend: environment: APP_URL_API: url.backendURL

url: {
	frontendURL: frontend.site.url              @dagger(output)
	backendURL:  "https://\(backend.hostname)/" @dagger(output)
}
