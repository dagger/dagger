package netlify


#dag: {
	do: [
		{
			action: "fetch"
			type: "container"
			repository: "alpine"
			tag: "latest"
		},
		{
			action: "run"
			command: "apk add ..."
		},
		{
			action: "copy"
			from: [
				{
					action: "fetch"
					type: "git"
					repo: "https://github.com/shykes/stuff"
				}
			]
			source: "/"
			dest: "/src"
		},
		
	]
}

// Name of the netlify site
name: {
	string

	#dag: {

	}
}

// ID of the netlify site
// FIXME: compute
id: {
	string

	#dag: {
		from: ...
		do: [
			action: "run"
			command: ["netlify-get-id", name, "-o", "/netlify-id.txt"]
		]
		export: string: "/netlify-id.txt"
	}
}

// API token
// FIXME: encrypt secret!
token: {
	#encrypt: {
		pubkey: _
		cipher: _
	}
	string
}

// FIXME: how to receive a directory?
source: bl.#Dir


// Domain of the Netlify site
domain?: string

// FIXME: compute
url: {

	#dag: {
		do: [
			// ...
			{
				action: "run"
				command: "netlify deploy"
				dir: "/src"
				mount: "/src": source
			}
		]
	}

	string
}
