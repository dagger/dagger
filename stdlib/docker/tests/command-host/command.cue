package docker

TestConfig: {
	host: string @dagger(input)
}

TestHost: client: #Command & {
	command: #"""
			docker $CMD
		"""#
	host: TestConfig.host
	env: CMD: "version"
}
