package dagger


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
