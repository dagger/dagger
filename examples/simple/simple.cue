// ACME platform: everything you need to develop and ship improvements to
// the ACME clothing store.
package acme

import (
	"dagger.cloud/alpine"
	"dagger.cloud/dagger"
)

let base=alpine & {
	package: {
		bash: ">3.0"
		rsync: true
	}
}

www: {

	source: {
		// Make this undefined on purpose to require an input directory.
		#dagger: compute: _
	}

	host: string

	url: {
		string

		#dagger: compute: [
			dagger.#Load & { from: base },
			dagger.#Exec & {
				args: ["sh", "-c", "echo -n 'https://\(host)/foo' > /tmp/out"]
				// https://github.com/blocklayerhq/dagger/issues/6
				mount: foo: {}
			},
			dagger.#Export & {
				// https://github.com/blocklayerhq/dagger/issues/8
				// source: "/tmp/out"
			},
		]
	}
}
