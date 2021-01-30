// ACME platform: everything you need to develop and ship improvements to
// the ACME clothing store.
package acme

import (
	"dagger.cloud/dagger"
)

let alpine={
	digest: "sha256:3c7497bf0c7af93428242d6176e8f7905f2201d8fc5861f45be7a346b5f23436"
	package: [string]: true | false | string
	#dagger: compute: [
		{
			do: "fetch-container"
			ref: "index.docker.io/alpine@\(digest)"
		},
		for pkg, info in package {
			if (info & true) != _|_ {
				do: "exec"
				args: ["apk", "add", "-U", "--no-cache", pkg]
			}
			if (info & string) != _|_  {
				do: "exec"
				args: ["apk", "add", "-U", "--no-cache", "\(pkg)\(info)"]
			}
		},
	]
}

www: {

	source: dagger.#Dir

	// List the contents of the source directory
	listing: {
		string

		#dagger: compute: [
			dagger.#Load & {
				from: alpine
			},
			dagger.#Exec & {
				args: ["sh", "-c", "ls /src > /tmp/out"]
				mount: "/src": {
					from: source
				}
			},
			dagger.#Export & {
				source: "/tmp/out"
			}
		]
	}

	host: string

	url: {
		string

		#dagger: compute: [
			{
				do: "load"
				from: alpine
			},
			dagger.#Exec & {
				args: ["sh", "-c", "echo -n 'https://\(host)/foo' > /tmp/out"]
			},
			dagger.#Export & {
				source: "/tmp/out"
			},
		]
	}
}
