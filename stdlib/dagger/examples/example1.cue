package dagger

dev: dagger.#Plan & {

	context: {

		// Docker engine endpoint
		services: docker: _

		// Netlify API token
		secrets: netlify: _

		// Frontend source code
		directories: frontend: _
	}

	actions: {

		sharedCache: dagger.#CacheDir & {
			concurrency: "locked"
		}

		foo: dagger.#Exec & {
			exec: {
				command: "echo foo"
				mount: {
					"/cache": sharedCache
					"/var/run/docker.sock": context.services.docker
				}
			}
		}

		bar: dagger.#Exec & {
			exec: {
				command: "echo bar"
				mount: {
					"/cache": sharedCache
					"/netlify-token": context.secrets.netlify
				}
			}
		}

	}

}
