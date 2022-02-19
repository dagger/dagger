package go

// Test a go package
#Test: {
	// Package to test
	package: *"." | string

	#Container & {
		command: {
			args: [package]
			flags: {
				test: true
				"-v": true
			}
		}
	}
}
