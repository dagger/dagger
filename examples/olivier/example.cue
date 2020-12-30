package example

import (
	"dagger.cloud/alpine"
)

test: {
	string
	#dagger: compute: [
		{ do: "load", from: alpine },
		{
			do: "copy"
			from: [
				{ do: "fetch-container", ref: alpine.ref },
			]
			dest: "/src"
		},
		{
			do: "exec"
			dir: "/src"
			args: ["sh", "-c", """
				ls -l > /tmp/out
				"""
			]
		},
		{
			do: "export"
			source: "/tmp/out"
			format: "string"
		}
	]
}

www: {

	// Domain where the site will be deployed (user input)
	domain: string

	// URL after deployment (computed)
	url: {
		string & =~ "https://.*"

		#dagger: {
			compute: [
				{ do: "load", from: alpine },
				{
					do: "exec"
					args: ["sh", "-c",
						"""
						echo 'deploying to netlify (not really)'
						echo 'https://\(domain)/foo' > /tmp/out
						"""
					]
				},
				{
					do: "export"
					source: "/tmp/out"
					format: "string"
				}
			]
		}
	}
}
