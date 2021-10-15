package todoapp

import (
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/os"
	"alpha.dagger.io/docker"
	"alpha.dagger.io/js/yarn"
)

// Build the source code using Yarn
app: yarn.#Package & {
	source: dagger.#Artifact & dagger.#Input
}

// package the static HTML from yarn into a Docker image
image: os.#Container & {
	image: docker.#Pull & {
		from: "nginx"
	}

	// app.build references our app key above
	// which infers a dependency that Dagger
	// uses to generate the DAG
	copy: "/usr/share/nginx/html": from: app.build
}
