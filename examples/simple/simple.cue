// ACME platform: everything you need to develop and ship improvements to
// the ACME clothing store.
package acme

import (
	"dagger.cloud/alpine"
)

let base=alpine & {
	package: {
		bash: ">3.0"
		rsync: true
	}
}

www: {

	source: {
		#dagger: compute: _
	}

	host: string

	url: {
		string

		#dagger: compute: [
			{ do: "load", from: base },
			{ do: "exec", args: ["sh", "-c", "echo -n 'https://\(host)/foo' > /tmp/out"] },
			{ do: "export", format: "string", source: "/tmp/out" },
		]
	}
}
