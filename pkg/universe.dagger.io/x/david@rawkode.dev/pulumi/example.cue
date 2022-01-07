package rawkode_pulumi_example

import (
	"dagger.io/dagger"
	"universe.dagger.io/x/david@rawkode.dev/pulumi"
)

dagger.#Plan & {
	client: {
		filesystem: {
			"./": read: {
				contents: dagger.#FS
			}
		}
		env: {
			PULUMI_CONFIG_PASSPHRASE: dagger.#Secret
			PULUMI_ACCESS_TOKEN:      dagger.#Secret
		}
	}
	actions: {
		rawkode: pulumi.#Up & {
			stack:       "test"
			stackCreate: true
			runtime:     "nodejs"
			accessToken: client.env.PULUMI_ACCESS_TOKEN
			source:      client.filesystem."./".read.contents
		}
	}
}
