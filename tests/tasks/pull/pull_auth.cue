package main

import (
	"alpha.dagger.io/europa/dagger/engine"
)

engine.#Plan & {
	context: secrets: dockerHubToken: envvar: "DOCKERHUB_TOKEN"
	actions: pull: engine.#Pull & {
		source: "daggerio/ci-test:private-pull@sha256:c74f1b1166784193ea6c8f9440263b9be6cae07dfe35e32a5df7a31358ac2060"
		auth: [{
			target:   "daggerio/ci-test:private-pull"
			username: "daggertest"
			secret:   context.secrets.dockerHubToken.contents
		}]
	} & {
		// assert result
		digest: "sha256:c74f1b1166784193ea6c8f9440263b9be6cae07dfe35e32a5df7a31358ac2060"
		config: {
			Env: ["PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"]
			Cmd: ["/bin/sh"]
		}
	}
}
