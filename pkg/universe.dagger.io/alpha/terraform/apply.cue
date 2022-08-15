package terraform

// Run `terraform apply`
#Apply: {

	// The Version of Terraform cli to use
	version?: string

	#Run & {
		// Terraform `apply` command
		cmd: "apply"

		// Internal pre-defined arguments for `terraform apply`
		withinCmdArgs: [
			if autoApprove {
				"-auto-approve"
			},
			planFile,
		]

		// Flag to indicate whether or not to auto-approve (i.e. -auto-approve flag)
		autoApprove: bool | *true

		// Path to a Terraform plan file
		planFile: string | *_#DefaultPlanFile

		"version": version
	}
}
