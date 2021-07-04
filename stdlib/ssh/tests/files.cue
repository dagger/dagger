package ssh

import (
	"alpha.dagger.io/dagger"
)

TestArtifact: dagger.#Artifact & dagger.#Input

TestSecret: dagger.#Secret & dagger.#Input

TestConfig: {
	host: string         @dagger(input)
	user: string         @dagger(input)
	key:  dagger.#Secret @dagger(input)
}

TestFiles: {
	home: "/home/daggerci"

	dataPath: "\(home)/data"

	secretPath: "\(home)/secret"

	// Upload files to remote host
	files: #Files & {
		sshConfig: {
			host: TestConfig.host
			user: TestConfig.user
			key:  TestConfig.key
		}
		files: "\(dataPath)":     TestArtifact
		secrets: "\(secretPath)/secret.txt": TestSecret
	}

	// Cleanup remote files
	cleanup: {
		filesClean: #Cleanup & {
			sshConfig: files.sshConfig
			target:    "\(dataPath)"
		}

		secretsClean: #Cleanup & {
			sshConfig: files.sshConfig
			target:    "\(secretPath)"
		}
	}
}
