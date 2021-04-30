package main

// Inuitively, MyPort is the input
#MyStr: string & =~"[d]{4:5}"
#MyInt: int & >1024
#MyPort: #MyStr | #MyInt

port: #MyPort

site: {
	// certainly an input
	name: string
	// is this an input despite the reference?
	_envs: ["dev", "stg", "qa"]
	subdomain: or(_envs)
	// not an input
	domain: "\(name).\(subdomain).domain.com"
}

app: {
	enabled: {
		db: bool | *true
		cache: bool | *false
	}

	if enabled.db {
		db: {
			host: string
			port: string
		}
	}

	// should the user see this despite the default false (which is displayed)
	if enabled.cache {
		cache: {
			host: string
			port: string
		}
	}
}

// Is auth a common schema or a shared configuration?
#auth: {
	user: string
	pass: string
}
#API_1: {
	auth: #auth
	#up: []
}
#API_2: {
	auth: #auth
	#up: []
}

