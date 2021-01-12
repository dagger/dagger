package example

import (
	"dagger.cloud/alpine"
	"dagger.cloud/dagger"
)

test: {
	string
	#dagger: compute: [
		dagger.#Load & { from: alpine },
		dagger.#Copy & {
			from: [
				dagger.#FetchContainer & { ref: alpine.ref },
			]
			dest: "/src"
			// https://github.com/blocklayerhq/dagger/issues/9
			src: "/"
		},
		dagger.#Exec & {
			dir: "/src"
			args: ["sh", "-c", """
				ls -l > /tmp/out
				"""
			]
			// https://github.com/blocklayerhq/dagger/issues/6
			mount: foo: {}
			// mount: dagger.#Mount
		},
		dagger.#Export & {
			// https://github.com/blocklayerhq/dagger/issues/8
			// source: "/tmp/out"
		},
	]
}

www: {
	// Domain where the site will be deployed (user input)
	domain: string

	url: {
		string & =~ "https://.*"

		// https://github.com/blocklayerhq/dagger/issues/10
		#dagger2: compute: [
			dagger.#Load & { from: alpine },
			dagger.#Exec & {
				args: ["sh", "-c",
					"""
					echo 'deploying to netlify (not really)'
					echo 'https://\(domain)/foo' > /tmp/out
					"""
				]
				// https://github.com/blocklayerhq/dagger/issues/6
				mount: foo: {}
			},
            dagger.#Export & {
        		// https://github.com/blocklayerhq/dagger/issues/8
                // source: "/tmp/out"
            }
        ]
    }
}
