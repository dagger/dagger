package testing

test: {

	#dagger: {
		compute: [
			{
				do: "fetch-container"
				ref: "alpine"
			},
			{
				do: "exec"
				args: ["sh", "-c", """
				echo "--- # Shopping list
				[milk, pumpkin pie, eggs, juice]" > /tmp/out
				"""
				]
				// XXX Blocked by https://github.com/blocklayerhq/dagger/issues/19
				dir: "/"
			},
			{
				do: "export"
				// Source path in the container
				source: "/tmp/out"
				format: "yaml"
			},
		]
	}
}
