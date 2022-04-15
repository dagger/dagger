package go

// Test a go package
#Test: {
	// DEPRECATED: use packages instead
	package: string | *null

	// Packages to test
	packages: [...string]

	#Container & {
		command: {
			_packages: [...string] | []
			if package == null && len(packages) == 0 {
				_packages: ["."]
			}
			if package != null && len(packages) == 0 {
				_packages: [package]
			}
			if package == null && len(packages) > 0 {
				_packages: packages
			}
			if package != null && len(packages) > 0 {
				_packages: [package] + packages
			}

			name: "go"
			args: _packages
			flags: {
				test: true
				"-v": true
			}
		}
	}
}
