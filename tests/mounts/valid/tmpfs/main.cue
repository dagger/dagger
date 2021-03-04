package testing

test: {
	string

	#dagger: compute: [
		{
			do: "load"
			from: [{do: "fetch-container", ref: "alpine"}]
		},
		{
			do: "exec"
			args: ["sh", "-c", """
				echo ok > /out
				echo ok > /tmpdir/out
				"""]
			dir: "/"
			mount: "/tmpdir": "tmpfs"
		},
		{
			do: "exec"
			args: ["sh", "-c", """
			[ -f /out ] || exit 1
			# content of /cache/tmp must not exist in this layer
			[ ! -f /tmpdir/out ] || exit 1
			"""]
		},
		{
			do:     "export"
			source: "/out"
			format: "string"
		},
	]
}
