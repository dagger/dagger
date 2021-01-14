package acme

www: {

	dosomething: {
		#dagger: compute: [
			{ do: "fetch-container", ref: "index.docker.io/redis"}
		]
	}
}

bar: {
	dosomethingelse: {
		#dagger: compute: [
			{
				do: "fetch-git"
				remote: "https://github.com/shykes/tests"
				ref: "stdlib"
			}
		]
	}
}
