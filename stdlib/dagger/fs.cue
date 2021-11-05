package dagger


// Assemble a filesystem
#FS: {
	root: #Copyable | *null
	copy: [path=string]: {
		source: #Copyable
		subdir?: string
	}
	mount: [path=string]: #CacheDir | #TempDir | #Service | {
		from: #Copyable
		readOnly: true | *false
	} | {
		from: #Secret
		uid: uint32 | *0
		gid: uint32 | *0
		optional: true | *false
	}
}

#Copyable: #OCIPull | #GitPull | #ContextPull | #Exec

// A (best effort) persistent cache dir
#CacheDir: {
	// Reserved for runtime use
	_cacheDirID: string

	concurrency: *"shared" | "private" | "locked"
}

// A temporary directory for command execution
#TempDir: {
	// Reserved for runtime use
	_tempDirID: string

	size?: int64
}
