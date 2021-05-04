package io

// Standard interface for directory operations in cue
#Dir: {
	read: tree: string
	...
}

// Standard interface for file operations in cue
#File: {
	#Reader
	#Writer
	...
}

#ReadWriter: #Reader & #Writer

#Reader: {
	read: {
		// FIXME: support different data schemas for different formats
		format: "string" | "json" | "yaml" | "lines"
		data: {
			string
			...
		}
	}
	...
}

#Writer: {
	write: *null | {
		// FIXME: support writing in multiple formats
		// FIXME: append
		data: _
	}
	...
}
