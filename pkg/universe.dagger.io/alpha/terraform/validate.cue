package terraform

// Run `terraform validate`
#Validate: {

	// The Version of Terraform cli to use
	version?: string

	#Run & {
		// Terraform `validate` command
		cmd: "validate"

		"version": version
	}
}
