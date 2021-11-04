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
}

actions: {
	build: {
		source: context.directories.monorepo
	}
}
