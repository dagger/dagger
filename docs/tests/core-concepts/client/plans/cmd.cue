dagger.#Plan & {
	client: commands: {
		os: {
			name: "uname"
			args: ["-s"]
		}
		arch: {
			name: "uname"
			args: ["-m"]
		}
	}

	actions: build: go.#Build & {
		os:   client.commands.os.stdout
		arch: client.commands.arch.stdout
		// ...
	}
}
