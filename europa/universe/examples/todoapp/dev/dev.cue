// Local dev environment for todoapp
package todoapp

import (
	"universe.dagger.io/docker"
	"universe.dagger.io/nginx"
)

// Expose todoapp web port
proxy: web: _

actions: {
	// Reference app build inherited from base config
	build: _
	_app:  build.output

	container: {
		// Build a container image serving the app with nginx
		build: docker.#Build & {
			steps: [
				nginx.#Build & {
					flavor: "alpine"
				},
				docker.#Copy & {
					contents: _app
					dest:     "/usr/share/nginx/html"
				},
			]
		}

		// Run the app in an ephemeral container
		run: docker.#Run & {
			image: build.output
			ports: web: {
				frontend: proxy.web.endpoint
				backend: address: "localhost:5000"
			}
		}
	}
}
