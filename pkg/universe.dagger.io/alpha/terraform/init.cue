package terraform

// Run `terraform init`
#Init: {

	// The Version of Terraform cli to use
	version?: string

	#Run & {
		// Terraform `init` command
		cmd: "init"

		"version": version
	}
}
