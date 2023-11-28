package python

// Simple wrapper to run a python script in a container
#RunBase: {
	// directory containing script to run
	directory: dagger.#FS

	// name of script to execute
	filename: string

	// arguments to the script
	args: [...string]

	// where to mount the script inside the container
	_mountpoint: "/run/python"

	docker.#Run & {
		command: {
			name: "python"
			// string concatenation of _mountpoint and filename variables
			"args": ["\(_mountpoint)/\(filename)"] + args
		}

		mounts: "Python script": {
			contents: directory
			// where to mount the script inside the container
			dest: _mountpoint
		}
	}
}
