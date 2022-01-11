// IO operations
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

// Standard ReadWriter interface
#ReadWriter: #Reader & #Writer

// Standard Reader interface
#Reader: {
	read: {
		// FIXME: support different data schemas for different formats
		format: "string" | "json" | "yaml" | "lines"
		data: {
			string
		}
	}
	...
}

// Standard Writer interface
#Writer: {
	write: *null | {
		// FIXME: support writing in multiple formats
		// FIXME: append
		data: _
	}
	...
}
