package dagger

// Push a filesystem tree to an OCI repository
#OCIPush: {
	// Reserved for runtime use
	_ociPushID: string

	// Filesystem contents to push
	fs: #FS
	// Repository target ref
	target: string
	// Authentication
	auth: #OCIAuth
	// Resulting digest after pushing
	digest: string
	// OCI metadata to upload
	metadata: #OCIMetadata
}

// Pull an OCI image from a remote repository
#OCIPull: {
	// Reserved for runtime use
	_ociPullID: string

	// Repository source ref
	source: string
	// Authentication
	auth: #OCIAuth
	// Downloaded OCI metadata
	metadata: #OCIMetadata
}

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

#OCIBuild: {
	// Reserved for runtime use
	_ociBuildID: string

	source: #FS
	{
		// Dockerfile frontend
		frontend: "dockerfile"
		dockerfilePath?: string
	}
	// Resulting OCI metadata
	metadata: #OCIMetadata
}
