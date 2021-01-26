package testing

foo: {
	new_prop: "lala"
	#new_def: "lala"

	new_prop_too: string
	#new_def_too: string

	#dagger: compute: [{
		do:  "fetch-container"
		ref: "busybox"
	},
		{
			do: "exec"
			args: ["true"]
			dir: "/"
		}]
}
