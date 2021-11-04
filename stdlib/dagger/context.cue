package dagger

// An external secret loaded at runtime
#Secret: {
	type: "secret"

	// Reserved for runtime use
	_id: string
}


// An external directory loaded at runtime
#LocalDir: {
	type: "localdir"

	// Reserved for runtime use
	_id: string
}

// An external socket loaded at runtime
#Service: {
	type: "service"

	// Reserved for runtime use
	_id: string
}

// A (best effort) persistent cache dir
// NOTE: buildkit can automatically create cache directories without
//  requiring user input via the 'context' key
#CacheDir: {
	type: "cachedir"

	concurrency: *"shared" | "private" | "locked"

	// Reserved for runtime use
	_id: string
}
