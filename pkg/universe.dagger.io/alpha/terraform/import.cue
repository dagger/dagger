package terraform

// Run `terraform import`
#Import: {
	// The `address` of the specified Terraform resource to import
	address: string

	// The `id` of the specified Terraform `address` to import
	id: string

	// The Version of Terraform cli to use
	version?: string

	#Run & {
		// Terraform `import` command
		cmd: "import"

		// Adding the `address` and `id` as positional arguments 
		cmdArgs: [address, id]

		"version": version
	}
}
