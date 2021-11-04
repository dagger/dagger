package dagger

// Assemble a filesystem
#FS: {
	// Reserved for runtime use
	_fsID: string

	[path=string]: #Mount | #Copy | string | bytes
}

#Mount: {
	// Reserved for runtime use
	_mountID: string

	readOnly: true | *false

	{
		source: #CacheDir
	} | {
		source: #TempDir
	} | {
		source: #Secret
		uid: uint32 | *0
		gid: uint32 | *0
		optional: true | *false
	} | {
		source: #Service
	} | {
		source: #Copyable
	}
}

#Copyable: #OCIPull | #GitPull | #ContextPull | #Exec

#Copy: {
	// Reserved for runtime use
	_copyID: string

	source: #Copyable
	subdir?: string
}

// A stream of bytes
#Stream: {
	// Reserved for runtime use
	_streamID: string
}

// An external secret
#Secret: {
	// Reserved for runtime use
	_secretID: string
}

// An external network service
#Service: {
	// Reserved for runtime use
	_serviceID: string
}

// Pull files from a context directory.
// Files are streamed via the builkdkit grpc transport.
#ContextPull: {
	// Reserved for runtime use
	_contextPullID: string

	include?: [...string]
	exclude?: [...string]
}
