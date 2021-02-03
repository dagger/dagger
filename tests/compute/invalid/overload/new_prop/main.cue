package testing

bar: #dagger: {

	new_prop: "lala"

	compute: [{
		do:  "fetch-container"
		ref: "busybox"
	},
		{
			do: "exec"
			args: ["true"]
			dir: "/"
		}]
}
