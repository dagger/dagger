package uffizzi

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"
	"universe.dagger.io/docker"
	"universe.dagger.io/bash"
)

#Run: {
	source: dagger.#FS

	uffizzi_user:     string
	uffizzi_password: dagger.#Secret
	uffizzi_project:  string | "default"
	uffizzi_server:   string | "https://app.uffizzi.com"

	docker_compose_file: string | "docker-compose.yaml"
	deployment_id:       string | *null

	dockerhub_username: string
	dockerhub_password: dagger.#Secret

	// Entity on which the command is being performed eg: project, preview etc
	entity: string
	// Verb, action to perform based on the given entity
	verb: string

	_source_path: "/src"
	version:      *"latest" | string

	_image: #Image

	bash.#Run & {
		input: *_image.output | docker.#Image
		script: {
			_load: core.#Source & {
				path: "."
				include: ["*.sh"]
			}
			directory: _load.output
			filename:  "uffizzi.sh"
		}

		env: {
			UFFIZZI_USER:       uffizzi_user
			UFFIZZI_SERVER:     uffizzi_server
			ENTITY:             entity
			VERB:               verb
			DOCKERHUB_USERNAME: dockerhub_username
			if uffizzi_project != null {UFFIZZI_PROJECT: uffizzi_project}
			if docker_compose_file != null {UFFIZZI_COMPOSE: "\(_source_path)/\(docker_compose_file)"}
			if deployment_id != null {DEPLOYMENT_ID: deployment_id}
		}
		workdir: _source_path
		mounts: {
			uffizzi_source: {
				dest:     _source_path
				contents: source
			}
			"uffizzi_password": {
				dest:     "/run/secrets/uffizzi_password"
				contents: uffizzi_password
			}
			"dockerhub_password": {
				dest:     "/run/secrets/dockerhub_password"
				contents: dockerhub_password
			}
		}
	}
}
