package docker

import (
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/random"
)

TestConfig: {
	host: string         @dagger(input)
	user: string         @dagger(input)
	key:  dagger.#Secret @dagger(input)
}

TestSSH: {
	suffix: random.#String & {
		seed: "run ssh"
	}

	app: #Run & {
		name: "daggerci-test-ssh-\(suffix.out)"
		ref:  "hello-world"
		ssh: {
			host: TestConfig.host
			user: TestConfig.user
			key:  TestConfig.key
		}
	}
}

TestArtifact: dagger.#Artifact @dagger(input)

TestRunArtifact: {
	suffix: random.#String & {
		seed: "run artifact"
	}

	app: #Run & {
		name: "daggerci-test-ssh-\(suffix.out)"
		ref:  "my-app"
		build: source: TestArtifact
		ssh: {
			host: TestConfig.host
			user: TestConfig.user
			key:  TestConfig.key
		}
	}
}
