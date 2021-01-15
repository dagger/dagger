package testing

#dagger: compute: [
	{
		do: "fetch-container"
		ref: "alpine"
	},
	{
		do: "exec"
		// XXX Blocked by https://github.com/blocklayerhq/dagger/issues/19
		dir: "/"
	},
}
