package terraform

// Subcommands of `terraform workspace` command
#WorkspaceSubcmd: "new" | "list" | "show" | "select" | "delete"

// Run `terraform workspace`
#Workspace: {
	// Terraform `workspace` subcommand (i.e. new, select, delete)
	subCmd: #WorkspaceSubcmd

	// The `name` of the specified Terraform `workspace` to perform the `subCmd` action on
	name?: string

	#Run & {
		// Terraform `workspace` command
		cmd: "workspace"

		// Adding the `subCmd` and `name` as positional arguments 
		if name != _|_ {
			cmdArgs: [subCmd, name]
		}

		if name == _|_ {
			cmdArgs: [subCmd]
		}
	}
}
