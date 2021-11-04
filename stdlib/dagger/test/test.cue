package dagger

#Plan

context: {
	secrets: netlify: {
		envvar: "NETLIFY_TOKEN"
	}
	directories: monorepo: {
		path: "."
	}
	services: docker: {
		unix: "/var/run/docker.sock"
	}
	settings: {
		// Path of web source code in monorepo
		web: string | *"www"
		// Path of API source code in monorepo
		api: string  | *"api"
	}
}

actions: {
	monorepo: #DAG & {
		fs: {
			context.directories.monorepo
			"/\(context.settings.web)": #FS
			"/\(context.settings.api)": #FS
		}
	}
	web: #DAG & {
		fs: 
	}
		source: c
	build: #Build & {
		source: context.directories.monorepo
		frontend: "dockerfile"
	}
	push: 
	test: #dockerRun {
		image: build.image
		command: "uname -a"
		engine: context.services.docker
	}
}


#dockerRun: {
	image: #FS
	command: string
	engine: #Service

	// Docker client
	client: #DAG & {
		fs: #Fetch & {
			oci: ref: "index.docker.io/docker"
		}
	}

	// Load the docker image
	load: client & {
		exec: {
			command: ["docker", "load", 
	}
	}

	_dag: {
		fs: 
		}
		exec: {
			cmd: command
			mount: "/var/run/docker.sock": engine
		}
	}
}
