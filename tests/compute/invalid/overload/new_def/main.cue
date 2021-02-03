package testing

bar: #dagger: {

	#new_def: "lala"

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
