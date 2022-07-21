// The doppler package makes it easy to fetch or update secrets using the 
// doppler.com SecretOps platform
package doppler

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"
	"universe.dagger.io/docker"
)

// Fetch a Config from Doppler
#FetchConfig: {
	// Doppler can be configured by a `doppler.yaml` file.
	// If you have one in your directory, you can pass it through with
	// core.#ReadFile.output
	configFile?: string

	// Doppler can be configured by a combination of project and config (environment)
	// You can provide these as strings
	project?: string
	config?:  string

	// The token to use in-order to authenticate against the Doppler API
	apiToken: dagger.#Secret

	// Steps
	imageName: string | *"dopplerhq/cli:3"

	_pullImage: docker.#Pull & {
		source: imageName
	}

	_fetchSecrets: docker.#Run & {
		input: _pullImage.output

		env: DOPPLER_TOKEN: apiToken

		entrypoint: ["ash"]
		command: {
			name: "-c"
			args: ["doppler secrets --json > /fetched-secrets.json"]
		}
	}

	_newSecret: core.#NewSecret & {
		input: _fetchSecrets.export.rootfs
		path:  "/fetched-secrets.json"
	}

	output: core.#DecodeSecret & {
		input:  _newSecret.output
		format: "json"
	}
}
