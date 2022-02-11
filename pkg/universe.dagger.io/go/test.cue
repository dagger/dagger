package go

// Test a go package
#Test: {
	// Package to test
	package: *"." | string

	#Container & {
		args: ["test", "-v", package]
	}
}
