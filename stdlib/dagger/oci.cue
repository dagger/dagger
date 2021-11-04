package dagger

// OCI-specific types

#OCIAuth: {
	[target=string]: {
		"target": target
		username: string
		secret:   string | #Secret
	}
}

// FIXME: OCI metadata schema
#OCIMetadata: {
	user:       string | *null
	workdir:    string | *null
	entrypoint: string | *null
	...
}
